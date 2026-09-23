package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/h2non/bimg"
)

var errCollectionArtworkInput = errors.New("invalid collection artwork input")

func validateCollectionImageData(data []byte) error {
	contentType := http.DetectContentType(data)
	switch contentType {
	case "image/jpeg", "image/png", "image/webp":
	default:
		return fmt.Errorf("unsupported image type: %s", contentType)
	}
	if _, err := bimg.NewImage(data).Size(); err != nil {
		return fmt.Errorf("invalid image: %w", err)
	}
	return nil
}

func writeCollectionArtworkError(w http.ResponseWriter, err error, internalMessage string) {
	if errors.Is(err, errCollectionArtworkInput) {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid collection artwork")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", internalMessage)
}
