package executor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/image/webp"

	"github.com/Silo-Server/silo-server/internal/scenariocatalog"
)

// The profile-list capability must be backed by actual writes and signed byte
// delivery, with the production account boundary intact on both transports.
func checkLocalAvatarFixture(t *testing.T, e *Env) {
	t.Helper()
	original := e.live
	func() {
		restore := e.withLocalAvatarRowFixture(scenariocatalog.Row{Method: "GET", Path: "/api/v1/profiles/"})
		defer restore()
		for _, version := range []string{"v1", "v2"} {
			t.Run(version, func(t *testing.T) {
				e.Reseed()
				defer e.Reseed()
				principal := scenariocatalog.Principal{Class: "primary_profile"}
				path := "/api/" + version + "/profiles/" + profileSecondary + "/avatar"
				request := scenariocatalog.Request{Path: path, Multipart: &scenariocatalog.Multipart{
					Files: []scenariocatalog.MultipartFile{{Field: "avatar", Filename: "fixture.png", ContentType: "image/png", Content: "png:320x192"}},
				}}
				exchange := func(method string, request scenariocatalog.Request, principal scenariocatalog.Principal, status int) response {
					t.Helper()
					expect := scenariocatalog.Expect{Status: status}
					if status == http.StatusNoContent {
						expect.BodyKind = bodyKindEmpty
					} else if method == http.MethodGet && status == http.StatusOK && strings.HasPrefix(request.Path, "/api/v2/artwork/") {
						// Decode and inspect the WebP below; image bytes are not JSON.
						expect.BodyKind = bodyKindAny
					}
					resp, failures, err := e.exchange(e.live.URL, method, request, principal, expect, nil, nil, nil)
					if err != nil || len(failures) != 0 {
						t.Fatalf("%s %s: err=%v failures=%v", method, request.Path, err, failures)
					}
					return resp
				}
				exchange(http.MethodPut, request, scenariocatalog.Principal{Class: "public"}, 401)
				foreign := request
				foreign.Path = "/api/" + version + "/profiles/" + profileAdminPrimary + "/avatar"
				exchange(http.MethodPut, foreign, principal, 404)
				resp := exchange(http.MethodPut, request, principal, 200)
				var profile struct {
					ID           string `json:"id"`
					AvatarSource string `json:"avatar_source"`
					AvatarURL    string `json:"avatar_url"`
				}
				if err := json.Unmarshal(resp.Raw, &profile); err != nil {
					t.Fatal(err)
				}
				if profile.ID != profileSecondary || profile.AvatarSource != "upload" {
					t.Fatal("upload did not return the owning profile's stored avatar")
				}
				var stored, foreignStored string
				if err := e.pool.QueryRow(e.ctx, `SELECT avatar FROM user_profiles WHERE id = $1`, profileSecondary).Scan(&stored); err != nil {
					t.Fatal(err)
				}
				prefix := fmt.Sprintf("profile-avatars/%d/%s/", e.users[fixtureMember].ID, profileSecondary)
				if stored != "upload:"+prefix+"original.webp" {
					t.Fatalf("stored avatar reference = %q", stored)
				}
				if err := e.pool.QueryRow(e.ctx, `SELECT avatar FROM user_profiles WHERE id = $1`, profileAdminPrimary).Scan(&foreignStored); err != nil || foreignStored != "" {
					t.Fatalf("foreign profile changed: err=%v", err)
				}
				u, err := url.Parse(profile.AvatarURL)
				if err != nil || u.IsAbs() || u.Host != "" || u.Path != "/api/v2/artwork/"+prefix+"w256.webp" || u.Query().Get("sig") == "" || u.Query().Get("exp") == "" {
					t.Fatal("local avatar did not return an owned signed artwork URL")
				}
				public := scenariocatalog.Principal{Class: "public"}
				bytesResponse := exchange(http.MethodGet, scenariocatalog.Request{Path: profile.AvatarURL}, public, 200)
				if bytesResponse.Headers.Get("Content-Type") != "image/webp" || bytesResponse.Headers.Get("Cache-Control") != "private, no-cache, no-transform" {
					t.Fatalf("avatar delivery headers: Content-Type=%q Cache-Control=%q", bytesResponse.Headers.Get("Content-Type"), bytesResponse.Headers.Get("Cache-Control"))
				}
				img, err := webp.Decode(bytes.NewReader(bytesResponse.Raw))
				if err != nil || img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
					t.Fatalf("avatar display bytes must decode as a 256px WebP: %v", err)
				}
				// Both variants exist. The display signature must not grant access
				// to the original under the same prefix.
				tampered := *u
				tampered.Path = strings.TrimSuffix(tampered.Path, "w256.webp") + "original.webp"
				exchange(http.MethodGet, scenariocatalog.Request{Path: tampered.String()}, public, 404)
				exchange(http.MethodGet, scenariocatalog.Request{Path: u.Path}, public, 404)
				deleteStatus := 200
				if version == "v2" {
					deleteStatus = 204
				}
				exchange(http.MethodDelete, scenariocatalog.Request{Path: path}, principal, deleteStatus)
				exchange(http.MethodGet, scenariocatalog.Request{Path: profile.AvatarURL}, public, 404)
			})
		}
	}()
	if e.live != original {
		t.Fatal("local avatar router survived row teardown")
	}
	// The row overlay must not advertise storage on the unconfigured router.
	resp, failures, err := e.exchange(e.live.URL, "GET", scenariocatalog.Request{Path: "/api/v1/profiles/"},
		scenariocatalog.Principal{Class: "primary_profile"}, scenariocatalog.Expect{Status: 200,
			Body: []scenariocatalog.BodyAssertion{{Pointer: "/avatar_upload_enabled", Op: "equals", Value: json.RawMessage(`false`)}}}, nil, nil, nil)
	if err != nil || len(failures) != 0 || !resp.IsJSON {
		t.Fatalf("unconfigured router changed after avatar fixture: err=%v failures=%v", err, failures)
	}
}
