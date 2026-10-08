package handlers

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

func TestReadCollectionImageMultipartRejectsUndecodableJPEG(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="poster"; filename="poster.jpg"`)
	header.Set("Content-Type", "image/jpeg")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("not actually a jpeg"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/collections", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	if _, err := readCollectionImageMultipart(request, "poster"); err == nil {
		t.Fatal("undecodable JPEG was accepted")
	}
}

type artworkCapableTestStore struct{ userstore.UserStore }

func (artworkCapableTestStore) CollectionFeatures() userstore.CollectionFeatures {
	return userstore.CollectionFeatures{Artwork: true}
}
