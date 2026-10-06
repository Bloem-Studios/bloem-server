package scanner

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	maxNativeEPUBDirectoryBytes = 8 << 20
	maxNativeEPUBMembers        = 8192
	nativeZIPTail               = 22 + 65535
)

// validateNativeEPUBIndex bounds archive/zip's allocation before it indexes any
// member. Metadata member limits alone do not bound the central directory.
// Multi-disk and ambiguous footer encodings are deliberately rejected.
func validateNativeEPUBIndex(reader io.ReaderAt, size int64) error {
	if size < 22 {
		return fmt.Errorf("invalid EPUB archive footer")
	}
	tail := make([]byte, min(size, int64(nativeZIPTail)))
	if err := readNativeEbookWindow(reader, tail, size-int64(len(tail))); err != nil {
		return err
	}
	end := -1
	for i := len(tail) - 22; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:]) == 0x06054b50 && i+22+int(binary.LittleEndian.Uint16(tail[i+20:])) == len(tail) {
			end = i
			break
		}
	}
	if end < 0 {
		return fmt.Errorf("invalid EPUB archive footer")
	}
	eocd := tail[end:]
	if bytes.Contains(eocd[4:], []byte{'P', 'K', 5, 6}) {
		return fmt.Errorf("ambiguous EPUB archive footer")
	}
	if binary.LittleEndian.Uint16(eocd[4:]) != 0 || binary.LittleEndian.Uint16(eocd[6:]) != 0 {
		return fmt.Errorf("multi-disk EPUB is unsupported")
	}
	records := uint64(binary.LittleEndian.Uint16(eocd[10:]))
	recordsDisk := uint64(binary.LittleEndian.Uint16(eocd[8:]))
	directorySize := uint64(binary.LittleEndian.Uint32(eocd[12:]))
	directoryOffset := uint64(binary.LittleEndian.Uint32(eocd[16:]))
	directoryEnd := size - int64(len(tail)) + int64(end)
	if records == 0xffff || recordsDisk == 0xffff || directorySize == 0xffffffff || directoryOffset == 0xffffffff {
		var err error
		records, recordsDisk, directorySize, directoryOffset, directoryEnd, err = nativeZIP64Directory(reader, directoryEnd)
		if err != nil {
			return err
		}
	}
	if records == 0 || records != recordsDisk || records > maxNativeEPUBMembers || directorySize > maxNativeEPUBDirectoryBytes {
		return fmt.Errorf("EPUB archive index exceeds native limits")
	}
	if directorySize > uint64(directoryEnd) || directoryOffset > uint64(directoryEnd) {
		return fmt.Errorf("invalid EPUB directory bounds")
	}
	start := directoryEnd - int64(directorySize)
	// archive/zip prefers the absolute offset when a valid header exists there,
	// otherwise it adjusts for an archive with a prepended prefix. Match that
	// choice, then validate the complete selected directory before indexing.
	if start > int64(directoryOffset) {
		var header [46]byte
		if err := readNativeEbookWindow(reader, header[:], int64(directoryOffset)); err != nil {
			return err
		}
		if binary.LittleEndian.Uint32(header[:]) == 0x02014b50 {
			start = int64(directoryOffset)
		}
	}
	if start < 0 || directorySize > uint64(size-start) {
		return fmt.Errorf("invalid EPUB directory bounds")
	}
	directory := make([]byte, int(directorySize))
	if err := readNativeEbookWindow(reader, directory, start); err != nil {
		return err
	}
	count := uint64(0)
	for len(directory) > 0 {
		if len(directory) < 46 || binary.LittleEndian.Uint32(directory) != 0x02014b50 {
			return fmt.Errorf("invalid EPUB central directory")
		}
		count++
		if count > maxNativeEPUBMembers {
			return fmt.Errorf("EPUB archive index exceeds native limits")
		}
		length := 46 + int(binary.LittleEndian.Uint16(directory[28:])) + int(binary.LittleEndian.Uint16(directory[30:])) + int(binary.LittleEndian.Uint16(directory[32:]))
		if length > len(directory) {
			return fmt.Errorf("truncated EPUB directory entry")
		}
		directory = directory[length:]
	}
	if count != records {
		return fmt.Errorf("EPUB directory member count differs")
	}
	return nil
}

func nativeZIP64Directory(reader io.ReaderAt, end int64) (records, recordsDisk, size, offset uint64, recordOffset int64, err error) {
	if end < 20 {
		err = fmt.Errorf("invalid ZIP64 EPUB locator")
		return
	}
	var locator [20]byte
	if err = readNativeEbookWindow(reader, locator[:], end-20); err != nil {
		return
	}
	if binary.LittleEndian.Uint32(locator[:]) != 0x07064b50 || binary.LittleEndian.Uint32(locator[4:]) != 0 || binary.LittleEndian.Uint32(locator[16:]) != 1 {
		err = fmt.Errorf("invalid ZIP64 EPUB locator")
		return
	}
	position := binary.LittleEndian.Uint64(locator[8:])
	if position > uint64(end-20) || uint64(end-20)-position < 56 {
		err = fmt.Errorf("invalid ZIP64 EPUB footer bounds")
		return
	}
	var footer [56]byte
	recordOffset = int64(position)
	if err = readNativeEbookWindow(reader, footer[:], recordOffset); err != nil {
		return
	}
	extra := binary.LittleEndian.Uint64(footer[4:])
	if binary.LittleEndian.Uint32(footer[:]) != 0x06064b50 || extra < 44 || extra > maxNativeEPUBDirectoryBytes || extra != uint64(end-20)-position-12 || binary.LittleEndian.Uint32(footer[16:]) != 0 || binary.LittleEndian.Uint32(footer[20:]) != 0 {
		err = fmt.Errorf("invalid ZIP64 EPUB footer")
		return
	}
	recordsDisk = binary.LittleEndian.Uint64(footer[24:])
	records = binary.LittleEndian.Uint64(footer[32:])
	size = binary.LittleEndian.Uint64(footer[40:])
	offset = binary.LittleEndian.Uint64(footer[48:])
	return
}

// Indexing reads are bounded as a second line of defense against alternative
// footer interpretations. Disable the budget once archive/zip has built its
// index; metadata and cover decompression have their separate member limits.
type nativeEPUBIndexReader struct {
	reader    io.ReaderAt
	remaining int64
	calls     int
	indexed   bool
}

func (r *nativeEPUBIndexReader) ReadAt(p []byte, offset int64) (int, error) {
	if !r.indexed {
		if int64(len(p)) > r.remaining || r.calls >= 4096 {
			return 0, fmt.Errorf("EPUB index read budget exceeded")
		}
		r.remaining -= int64(len(p))
		r.calls++
	}
	return r.reader.ReadAt(p, offset)
}
