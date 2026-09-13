package handlers

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/textproto"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/outbound"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type collectionArtworkResolver map[string][]netip.Addr

func (r collectionArtworkResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), r[host]...), nil
}

func TestDownloadCollectionImageURLRejectsPrivateDestinations(t *testing.T) {
	client := outbound.NewClient(
		outbound.PublicHTTPPolicy(),
		outbound.WithResolver(collectionArtworkResolver{
			"private.example": {netip.MustParseAddr("10.0.0.7")},
		}),
	)
	_, err := downloadCollectionImageURL(t.Context(), client, "http://private.example/poster.jpg")
	if err == nil {
		t.Fatal("private collection artwork source was accepted")
	}
}

func TestCreatePersonalCollectionReportsPrivateArtworkAsBadRequest(t *testing.T) {
	store := newPlaybackTestStore(t)
	handler := NewCollectionHandler(testUserStoreProvider{store: artworkCapableTestStore{store}})
	handler.ArtworkClient = outbound.NewClient(
		outbound.PublicHTTPPolicy(),
		outbound.WithResolver(collectionArtworkResolver{
			"private.example": {netip.MustParseAddr("10.0.0.7")},
		}),
	)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/collections", strings.NewReader(`{
		"name":"Private artwork",
		"collection_type":"manual",
		"poster_source_url":"http://private.example/poster.jpg"
	}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(newAuthorizedPlaybackContext())
	response := httptest.NewRecorder()

	handler.HandleCreateCollection(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"error":"bad_request"`) {
		t.Fatalf("body = %s, want typed bad_request", response.Body.String())
	}
}

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
