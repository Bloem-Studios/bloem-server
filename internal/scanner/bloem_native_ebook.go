package scanner

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Silo-Server/silo-server/internal/mediasource"
)

// NativeEbookMetadata uses the same fields and sanitization as local ebooks.
// Parsing does not publish catalog data or grant access to a storage source.
type NativeEbookMetadata = parsedEbook

// ParseNativeEbook reads an already opened, revision-pinned EPUB or PDF. The
// caller retains ownership of the file and must close it. LogicalPath is never
// opened on the filesystem, and sidecars must be discovered separately.
func ParseNativeEbook(ctx context.Context, file mediasource.File) (book NativeEbookMetadata, err error) {
	if ctx == nil || file == nil {
		return book, fmt.Errorf("native ebook requires a context and file")
	}
	if err = ctx.Err(); err != nil {
		return book, err
	}
	info := file.Info()
	if info.Size < 0 {
		return book, fmt.Errorf("invalid native ebook size")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic parsing native ebook: %v", recovered)
		}
		book.sanitize()
	}()
	reader := nativeEbookReaderAt{ctx: ctx, reader: file}
	switch ebookFileFormat(info.Name) {
	case ".epub":
		return parseNativeEPUB(reader, info.Size)
	case ".pdf":
		return parseNativePDF(reader, info.Size)
	default:
		return book, fmt.Errorf("unsupported native ebook format")
	}
}

type nativeEbookReaderAt struct {
	ctx    context.Context
	reader io.ReaderAt
}

func (r nativeEbookReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.ReadAt(p, offset)
	if contextErr := r.ctx.Err(); contextErr != nil {
		return n, contextErr
	}
	return n, err
}

func parseNativeEPUB(reader io.ReaderAt, size int64) (parsedEbook, error) {
	book := parsedEbook{Format: "epub"}
	if err := validateNativeEPUBIndex(reader, size); err != nil {
		return book, err
	}
	indexReader := &nativeEPUBIndexReader{reader: reader, remaining: maxNativeEPUBDirectoryBytes + 2*nativeZIPTail + 16384}
	archive, err := zip.NewReader(indexReader, size)
	indexReader.indexed = true
	if err != nil {
		return book, err
	}
	container, err := readEPUBZipEntry(archive, "META-INF/container.xml")
	if err != nil {
		return book, err
	}
	opfPath, err := epubOPFPath(container)
	if err != nil {
		return book, err
	}
	opf, err := readEPUBZipEntry(archive, opfPath)
	if err != nil {
		return book, err
	}
	if err := parseEPUBOPFMetadata(opf, &book); err != nil {
		return book, err
	}
	hasCover, err := nativeEPUBHasCover(opf)
	if err != nil {
		return book, err
	}
	if hasCover {
		// A missing optional cover is harmless. A declared cover's read/parse failure
		// must remain retryable; never publish a partial replacement on an outage.
		book.Cover, err = extractEPUBCover(archive, opfPath, opf)
		if err != nil {
			return book, err
		}
	}
	return book, nil
}

// The local extraction helper reports an absent optional cover with an ordinary
// error. Inspect only the reference here so provider failures need not be
// recognized by their error text or discarded along with optional absence.
func nativeEPUBHasCover(opf []byte) (bool, error) {
	var parsed struct {
		Metadata struct {
			Meta []struct {
				Name    string `xml:"name,attr"`
				Content string `xml:"content,attr"`
			} `xml:"meta"`
		} `xml:"metadata"`
		Manifest struct {
			Items []struct {
				ID         string `xml:"id,attr"`
				Href       string `xml:"href,attr"`
				MediaType  string `xml:"media-type,attr"`
				Properties string `xml:"properties,attr"`
			} `xml:"item"`
		} `xml:"manifest"`
	}
	if err := decodeEbookXML(opf, &parsed); err != nil {
		return false, err
	}
	coverID := ""
	for _, meta := range parsed.Metadata.Meta {
		if strings.EqualFold(strings.TrimSpace(meta.Name), "cover") {
			coverID = strings.TrimSpace(meta.Content)
			break
		}
	}
	for _, item := range parsed.Manifest.Items {
		if !isEPUBImageManifestItem(item.MediaType, item.Href) {
			continue
		}
		if coverID != "" && strings.TrimSpace(item.ID) == coverID {
			return true, nil
		}
		for _, property := range strings.Fields(strings.ToLower(item.Properties)) {
			if property == "cover-image" {
				return true, nil
			}
		}
	}
	return false, nil
}

func readNativeEbookWindows(reader io.ReaderAt, size int64) (head, tail []byte, err error) {
	head = make([]byte, min(size, int64(maxPDFMetadataScanSize)))
	if err = readNativeEbookWindow(reader, head, 0); err != nil {
		return nil, nil, err
	}
	if size <= int64(len(head)) {
		return head, nil, nil
	}
	tailStart := max(int64(len(head)), size-maxPDFMetadataScanSize)
	tail = make([]byte, size-tailStart)
	if err = readNativeEbookWindow(reader, tail, tailStart); err != nil {
		return nil, nil, err
	}
	return head, tail, nil
}

func parseNativePDF(reader io.ReaderAt, size int64) (parsedEbook, error) {
	book := parsedEbook{Format: "pdf"}
	head, tail, err := readNativeEbookWindows(reader, size)
	if err != nil {
		return book, err
	}
	encrypted, err := pdfDocumentEncrypted(reader, size)
	if err != nil {
		return book, err
	}
	if encrypted {
		return book, nil
	}
	info := parsePDFInfoFields(head)
	for key, value := range parsePDFInfoFields(tail) {
		if value != "" && info[key] == "" {
			info[key] = value
		}
	}
	book.Title = info["Title"]
	book.Authors = splitEbookAuthors(info["Author"])
	book.Description = info["Subject"]
	book.Genres = splitPDFKeywords(info["Keywords"])
	for _, value := range []string{info["ISBN"], info["Subject"], info["Keywords"], info["Title"]} {
		if isbn := normalizeEbookISBN(value); isbn != "" {
			book.ISBN = isbn
			break
		}
	}
	if published, ok := parsePDFDate(info["CreationDate"]); ok {
		book.PublishedAt = published
		book.Year = published.Year()
	}
	return book, nil
}

// ReadAt preserves an error even when all requested bytes were copied. ReadFull
// would discard that late error, accepting bytes whose final status failed.
func readNativeEbookWindow(reader io.ReaderAt, data []byte, offset int64) error {
	if len(data) == 0 {
		return nil
	}
	n, err := reader.ReadAt(data, offset)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrUnexpectedEOF
	}
	return nil
}
