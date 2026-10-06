package handlers

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"unicode"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/httpstream"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NativeEbookOpener resolves the authorized file's actual folder/binding and
// source reference, then opens a pinned file through an authorized native
// runtime snapshot. An installation ID alone must never grant source access.
type NativeEbookOpener interface {
	OpenAuthorizedEbook(context.Context, *models.MediaFile) (mediasource.File, error)
}

// NativeEbookFileService decorates the existing reader. It does not replace
// catalog/profile/PIN policy, and it never interprets a native key as a path.
type NativeEbookFileService struct {
	Local  *EbookReaderHandler
	Native NativeEbookOpener
}

func NewNativeEbookFileService(local *EbookReaderHandler, native NativeEbookOpener) *NativeEbookFileService {
	return &NativeEbookFileService{Local: local, Native: native}
}
func (h *NativeEbookFileService) ResolveReaderFile(ctx context.Context, contentID string, fileID int, filter catalog.AccessFilter) (*models.MediaFile, error) {
	if h == nil {
		return nil, nativeReaderUnavailable()
	}
	return h.Local.ResolveReaderFile(ctx, contentID, fileID, filter)
}
func nativeReaderUnavailable() *APIError {
	return &APIError{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "Native ebook source is unavailable"}
}
func nativeReaderError(err error) error {
	if errors.Is(err, catalog.ErrItemNotFound) || status.Code(err) == codes.NotFound {
		return catalog.ErrItemNotFound
	}
	if errors.Is(err, storagesource.ErrReferenceConflict) || status.Code(err) == codes.FailedPrecondition {
		return &APIError{Status: http.StatusConflict, Code: "conflict", Message: "Native ebook revision changed"}
	}
	if status.Code(err) == codes.PermissionDenied {
		return &APIError{Status: http.StatusForbidden, Code: "forbidden", Message: "Native ebook source access denied"}
	}
	return nativeReaderUnavailable()
}
func (h *NativeEbookFileService) ServeReaderFile(w http.ResponseWriter, r *http.Request, file *models.MediaFile) error {
	if h == nil || file == nil {
		return nativeReaderUnavailable()
	}
	if !strings.HasPrefix(file.FilePath, "bloem-storage:") {
		if h.Local == nil {
			return nativeReaderUnavailable()
		}
		return h.Local.ServeReaderFile(w, r, file)
	}
	format := ebookReaderFormat("", file.Container)
	if format != "epub" && format != "pdf" {
		return &APIError{Status: http.StatusUnsupportedMediaType, Code: "unsupported", Message: "Native reading supports EPUB and PDF"}
	}
	if h.Native == nil {
		return nativeReaderUnavailable()
	}
	opened, err := h.Native.OpenAuthorizedEbook(r.Context(), file)
	if opened != nil {
		defer opened.Close()
	}
	if err != nil {
		return nativeReaderError(err)
	}
	if opened == nil {
		return nativeReaderUnavailable()
	}
	info := opened.Info()
	if info.Size < 0 || info.Revision == "" || ebookReaderFormat(info.Name, "") != format {
		return nativeReaderUnavailable()
	}
	name := path.Base(strings.ReplaceAll(info.Name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if strings.TrimSpace(name) == "" || name == "." || name == "/" {
		name = "ebook." + format
	}
	// Hash the opaque revision, rather than exposing a provider's object identity.
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:%s", file.ID, info.Size, format, info.Revision)))
	w.Header().Set("ETag", fmt.Sprintf("\"%x\"", digest))
	w.Header().Set("Content-Type", ebookMimeType("", format))
	w.Header().Set("Content-Disposition", inlineContentDisposition(name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	attachTransfer(r.Context(), apimw.GetUserID(r.Context()), apimw.GetProfileID(r.Context()), file.ID)
	stream := &nativeEbookStream{File: opened}
	http.ServeContent(httpstream.NewRollingDeadlineWriter(w), r, name, info.ModifiedAt, stream)
	if stream.failure != nil {
		// Response framing may already be committed. Abort the stream: neither v1
		// nor v2 may append an error document to a partial binary response.
		panic(http.ErrAbortHandler)
	}
	return nil
}

type nativeEbookStream struct {
	mediasource.File
	failure error
}

func (s *nativeEbookStream) Read(p []byte) (int, error) {
	n, err := s.File.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		s.failure = err
	}
	return n, err
}
func (s *nativeEbookStream) Seek(offset int64, whence int) (int64, error) {
	result, err := s.File.Seek(offset, whence)
	if err != nil {
		s.failure = err
	}
	return result, err
}

// HandleReadFile supplies the existing v1 authentication, route and policy
// checks. Reader progress/config/annotations stay with the ordinary handler.
func (h *NativeEbookFileService) HandleReadFile(w http.ResponseWriter, r *http.Request) {
	if apimw.GetUserID(r.Context()) == 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}
	contentID := strings.TrimSpace(chi.URLParam(r, "content_id"))
	fileID, err := strconv.Atoi(chi.URLParam(r, "file_id"))
	if contentID == "" || err != nil || fileID <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "content_id and file_id are required")
		return
	}
	file, err := h.ResolveReaderFile(r.Context(), contentID, fileID, requestAccessFilter(r))
	if err == nil {
		err = h.ServeReaderFile(w, r, file)
	}
	if err != nil {
		if h == nil || h.Local == nil {
			writeError(w, http.StatusServiceUnavailable, "unavailable", "Ebook reader is not configured")
			return
		}
		h.Local.writeReadError(w, err)
	}
}
