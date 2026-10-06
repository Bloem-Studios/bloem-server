package scanner

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/mediasource"
)

func nativeParserFixture(t *testing.T, path string) mediasource.File {
	t.Helper()
	f, err := mediasource.OpenLocal(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestNativeEbookMetadataMatchesLocal(t *testing.T) {
	epub := writeTestEPUB(t, []string{"ISBN: 978-0-306-40615-7"})
	cover := writeTestEPUBWithCover(t, "Images/cover.jpg", "image/jpeg", []byte("embedded-cover"))
	pdf := filepath.Join(t.TempDir(), "book.pdf")
	if err := os.WriteFile(pdf, completePDFMetadataFixture([]byte("%PDF-1.7\n1 0 obj\n<< /Title (Useful Book) /Author (Ada Writer; Ben Author) /Subject (ISBN 978-0-306-40615-7) /CreationDate (D:20240102030405Z) >>\nendobj\n")), 0600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{epub, cover, pdf} {
		t.Run(filepath.Base(filepath.Dir(p)), func(t *testing.T) {
			want, err := parseEbookFile(p)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ParseNativeEbook(context.Background(), nativeParserFixture(t, p))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("native metadata %+v, local %+v", got, want)
			}
		})
	}
}

type nativeTestFile struct {
	*bytes.Reader
	info  mediasource.Info
	calls []struct {
		Offset int64
		Size   int
	}
	failOffset int64
	failure    error
}

func (f *nativeTestFile) Info() mediasource.Info { return f.info }
func (f *nativeTestFile) Close() error           { return nil }
func (f *nativeTestFile) ReadAt(p []byte, off int64) (int, error) {
	f.calls = append(f.calls, struct {
		Offset int64
		Size   int
	}{off, len(p)})
	if f.failure != nil && off >= f.failOffset {
		return 0, f.failure
	}
	return f.Reader.ReadAt(p, off)
}
func nativeBytes(name string, data []byte) *nativeTestFile {
	return &nativeTestFile{Reader: bytes.NewReader(data), info: mediasource.Info{Name: name, LogicalPath: "bloem-storage:opaque", Revision: "immutable", Size: int64(len(data))}}
}

func TestNativeEbookNeverUsesFilesystemSidecars(t *testing.T) {
	p := writeTestEPUB(t, []string{"ISBN: 978-0-306-40615-7"})
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ebookOPFSidecarPathForTest(p), []byte("malformed sidecar"), 0600); err != nil {
		t.Fatal(err)
	}
	f := nativeBytes("book.epub", data)
	f.info.LogicalPath = p
	got, err := ParseNativeEbook(context.Background(), f)
	if err != nil || got.Title != "The Test Ebook" {
		t.Fatalf("native metadata %+v, %v", got, err)
	}
}
func ebookOPFSidecarPathForTest(p string) string { return filepath.Join(filepath.Dir(p), "book.opf") }

func TestNativeEbookProviderFailureAndCancellation(t *testing.T) {
	sentinel := errors.New("provider unavailable")
	for _, name := range []string{"book.epub", "book.pdf"} {
		t.Run(name, func(t *testing.T) {
			f := nativeBytes(name, bytes.Repeat([]byte{'x'}, 100))
			f.failure = sentinel
			if _, err := ParseNativeEbook(context.Background(), f); !errors.Is(err, sentinel) {
				t.Fatalf("error %v, want provider error", err)
			}
			f.failure = nil
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := ParseNativeEbook(ctx, f); !errors.Is(err, context.Canceled) {
				t.Fatalf("error %v, want cancellation", err)
			}
		})
	}
}

func TestNativePDFWindowsBoundedAndNonoverlapping(t *testing.T) {
	data := completePDFMetadataFixture(append([]byte("%PDF-1.7\n1 0 obj\n<< /Title (Bounded Book) >>\nendobj\n"), bytes.Repeat([]byte{'x'}, 5*maxPDFMetadataScanSize)...))
	f := nativeBytes("large.pdf", data)
	got, err := ParseNativeEbook(context.Background(), f)
	if err != nil || got.Title != "Bounded Book" {
		t.Fatalf("metadata %+v, %v", got, err)
	}
	var total int
	for _, call := range f.calls {
		if call.Size > maxPDFMetadataScanSize {
			t.Fatalf("unbounded request %d", call.Size)
		}
		total += call.Size
		if call.Offset > maxPDFMetadataScanSize && call.Offset < int64(len(data)-maxPDFMetadataScanSize) {
			t.Fatalf("unnecessary body scan at %d", call.Offset)
		}
	}
	if total > 7*maxPDFMetadataScanSize {
		t.Fatalf("too much metadata data: %d", total)
	}
}

func TestNativeEbookRejectsUnsupportedAndInvalidPDF(t *testing.T) {
	for _, name := range []string{"book.mobi", "book.azw3", "book.cbz", "book.fb2"} {
		if _, err := ParseNativeEbook(context.Background(), nativeBytes(name, []byte("data"))); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	if _, err := ParseNativeEbook(context.Background(), nativeBytes("bad.pdf", []byte("%PDF-1.7\ntruncated"))); err == nil {
		t.Fatal("accepted invalid PDF trailer")
	}
	//nolint:staticcheck // Deliberately exercise rejection of an absent context.
	if _, err := ParseNativeEbook(nil, nativeBytes("book.pdf", nil)); err == nil {
		t.Fatal("accepted absent context")
	}
	if _, err := ParseNativeEbook(context.Background(), nil); err == nil {
		t.Fatal("accepted absent file")
	}
}

func TestNativePDFSuppressesEncryptedMetadata(t *testing.T) {
	got, err := ParseNativeEbook(context.Background(), nativeBytes("encrypted.pdf", pdfTrailerFixture("<< /Size 3 /Encrypt 9 0 R >>", "table")))
	if err != nil || got.Title != "" || got.Format != "pdf" {
		t.Fatalf("encrypted metadata %+v, %v", got, err)
	}
}

func TestNativePDFReadFailureNearTrailer(t *testing.T) {
	data := completePDFMetadataFixture(append([]byte("%PDF-1.7\n1 0 obj\n<< /Title (No partial publication) >>\nendobj\n"), bytes.Repeat([]byte{'x'}, 3*maxPDFMetadataScanSize)...))
	f := nativeBytes("book.pdf", data)
	f.failure = io.ErrUnexpectedEOF
	f.failOffset = maxPDFMetadataScanSize
	if _, err := ParseNativeEbook(context.Background(), f); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error %v, want read failure", err)
	}
}

// io.ReadFull drops a late provider error when n equals the requested size.
// Parsing must not promote those unvalidated bytes into usable metadata.
type nativeLateFailure struct {
	*nativeTestFile
	failure error
	used    bool
}

func (f *nativeLateFailure) ReadAt(p []byte, off int64) (int, error) {
	n, err := f.nativeTestFile.ReadAt(p, off)
	if !f.used {
		f.used = true
		return n, f.failure
	}
	return n, err
}
func TestNativePDFFullCountErrorRemainsFailure(t *testing.T) {
	data := completePDFMetadataFixture([]byte("%PDF-1.7\n1 0 obj\n<< /Title (Unvalidated Book) >>\nendobj\n"))
	sentinel := errors.New("late final status failed")
	f := &nativeLateFailure{nativeTestFile: nativeBytes("book.pdf", data), failure: sentinel}
	if _, err := ParseNativeEbook(context.Background(), f); !errors.Is(err, sentinel) {
		t.Fatalf("error %v, want late provider failure", err)
	}
}

type nativeCoverFailure struct {
	*nativeTestFile
	offset  int64
	failure error
}

func (f *nativeCoverFailure) ReadAt(p []byte, off int64) (int, error) {
	if off == f.offset {
		return 0, f.failure
	}
	return f.nativeTestFile.ReadAt(p, off)
}
func TestNativeEPUBDeclaredCoverFailureRemainsRetryable(t *testing.T) {
	path := writeTestEPUBWithCover(t, "Images/cover.jpg", "image/jpeg", []byte("embedded-cover"))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var offset int64
	for _, member := range archive.File {
		if member.Name == "OPS/Images/cover.jpg" {
			offset, err = member.DataOffset()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if offset == 0 {
		t.Fatal("missing cover fixture")
	}
	sentinel := errors.New("cover range unavailable")
	f := &nativeCoverFailure{nativeTestFile: nativeBytes("book.epub", data), offset: offset, failure: sentinel}
	if _, err := ParseNativeEbook(context.Background(), f); !errors.Is(err, sentinel) {
		t.Fatalf("error %v, want cover failure", err)
	}
}

func nativeEPUBWithExtraMembers(t *testing.T, count int) []byte {
	t.Helper()
	path := writeTestEPUB(t, nil)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	original, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, f := range original.File {
		entry, err := writer.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		reader, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(entry, reader)
		_ = reader.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < count; i++ {
		if _, err := writer.Create(fmt.Sprintf("unused/%d.txt", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func TestNativeEPUBRejectsUnboundedIndexBeforeAllocation(t *testing.T) {
	data := nativeEPUBWithExtraMembers(t, 9000)
	file := nativeBytes("book.epub", data)
	if _, err := ParseNativeEbook(context.Background(), file); err == nil {
		t.Fatal("accepted excessive EPUB member index")
	}
	if len(file.calls) > 3 {
		t.Fatalf("indexed excessive members using %d reads", len(file.calls))
	}
}
func TestNativeEPUBRejectsLyingIndexCountBeforeAllocation(t *testing.T) {
	data := nativeEPUBWithExtraMembers(t, 9000)
	eocd := len(data) - 22
	binary.LittleEndian.PutUint16(data[eocd+8:], 1)
	binary.LittleEndian.PutUint16(data[eocd+10:], 1)
	file := nativeBytes("book.epub", data)
	if _, err := ParseNativeEbook(context.Background(), file); err == nil {
		t.Fatal("accepted misleading EPUB member count")
	}
	if len(file.calls) > 3 {
		t.Fatalf("indexed misleading members using %d reads", len(file.calls))
	}
}

func nativeEPUBZIP64(t *testing.T, data []byte) []byte {
	t.Helper()
	position := len(data) - 22
	old := append([]byte(nil), data[position:]...)
	count := uint64(binary.LittleEndian.Uint16(old[10:]))
	size := uint64(binary.LittleEndian.Uint32(old[12:]))
	offset := uint64(binary.LittleEndian.Uint32(old[16:]))
	footer := make([]byte, 56)
	binary.LittleEndian.PutUint32(footer, 0x06064b50)
	binary.LittleEndian.PutUint64(footer[4:], 44)
	binary.LittleEndian.PutUint64(footer[24:], count)
	binary.LittleEndian.PutUint64(footer[32:], count)
	binary.LittleEndian.PutUint64(footer[40:], size)
	binary.LittleEndian.PutUint64(footer[48:], offset)
	locator := make([]byte, 20)
	binary.LittleEndian.PutUint32(locator, 0x07064b50)
	binary.LittleEndian.PutUint64(locator[8:], uint64(position))
	binary.LittleEndian.PutUint32(locator[16:], 1)
	binary.LittleEndian.PutUint16(old[8:], 0xffff)
	binary.LittleEndian.PutUint16(old[10:], 0xffff)
	binary.LittleEndian.PutUint32(old[12:], 0xffffffff)
	binary.LittleEndian.PutUint32(old[16:], 0xffffffff)
	result := append(append(append(append([]byte(nil), data[:position]...), footer...), locator...), old...)
	return result
}
func TestNativeEPUBIndexHandlesZIP64AndPrefix(t *testing.T) {
	path := writeTestEPUB(t, nil)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"ZIP64": nativeEPUBZIP64(t, original), "prefix": append([]byte("native-prefix:"), original...)} {
		t.Run(name, func(t *testing.T) {
			book, err := ParseNativeEbook(context.Background(), nativeBytes("book.epub", data))
			if err != nil || book.Title != "The Test Ebook" {
				t.Fatalf("metadata %+v, %v", book, err)
			}
		})
	}
}
func TestNativeEPUBIndexRejectsOversizedDirectoryAndMalformedZIP64(t *testing.T) {
	path := writeTestEPUB(t, nil)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	oversized := append([]byte(nil), original...)
	binary.LittleEndian.PutUint32(oversized[len(oversized)-22+12:], maxNativeEPUBDirectoryBytes+1)
	invalid := nativeEPUBZIP64(t, original)
	position := len(original) - 22
	binary.LittleEndian.PutUint64(invalid[position+32:], ^uint64(0))
	for _, data := range [][]byte{oversized, invalid} {
		file := nativeBytes("book.epub", data)
		if _, err := ParseNativeEbook(context.Background(), file); err == nil {
			t.Fatal("accepted invalid EPUB index")
		}
		if len(file.calls) > 4 {
			t.Fatalf("index performed %d reads", len(file.calls))
		}
	}
}
