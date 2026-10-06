package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/streamtelemetry"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

func TestNativeContainmentSignedProxyCollision(t *testing.T) {
	t.Chdir(t.TempDir())
	const native = "bloem-storage:malformed"
	const bytes = "unrelated-local-collision"
	if err := os.WriteFile(native, []byte(bytes), 0600); err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ path, method string }{{"/stream/direct/", "direct"}, {"/downloads/file/", streamtoken.PlayMethodDownload}} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, tc := range []struct {
				name, path string
				auth       error
				status     int
			}{
				{"native", native, nil, 404},
				{"local-basename", filepath.Join(".", native), nil, 404},
				{"ordinary", "./" + native, nil, 200},
				{"hidden", native, fmt.Errorf("wrapped: %w", ErrSourceHidden), 404},
				{"unavailable", native, fmt.Errorf("wrapped: %w", ErrAuthorityUnavailable), 503},
			} {
				// filepath.Join normalizes dot: its result is still the raw reserved identity.
				t.Run(route.method+"/"+method+"/"+tc.name, func(t *testing.T) {
					access := &fakeSourceAccess{err: tc.auth}
					srv, _ := newSourceAccessProxyServer(t, access)
					cfg := streamtelemetry.DefaultConfig("i1-proxy")
					cfg.Enabled = true
					telemetry := streamtelemetry.NewRegistry(cfg, streamtelemetry.NewLocalStore(), nil)
					srv.SetStreamTelemetry(telemetry)
					t.Cleanup(func() { _ = telemetry.Stop(t.Context()) })
					token, err := streamtoken.Sign(streamtoken.Claims{SessionID: "old-native-token", MediaPath: tc.path, PlayMethod: route.method, UserID: 7, MediaFileID: 42}, grantTestSecret, time.Minute)
					if err != nil {
						t.Fatal(err)
					}
					rr := httptest.NewRecorder()
					srv.Handler().ServeHTTP(rr, httptest.NewRequest(method, route.path+token, nil))
					if rr.Code != tc.status {
						t.Errorf("status=%d want=%d body=%q", rr.Code, tc.status, rr.Body.String())
					}
					if tc.status != 200 && (len(telemetry.Snapshot().Transfers) != 0 || len(telemetry.Snapshot().Sessions) != 0) {
						t.Error("denial attached transfer/session")
					}
					if access.calls != 1 {
						t.Errorf("authority calls=%d", access.calls)
					}
					if tc.status == 200 && method == "GET" && rr.Body.String() != bytes {
						t.Errorf("local bytes=%q", rr.Body.String())
					}
					if tc.status != 200 && (rr.Body.String() == bytes || rr.Header().Get("Content-Disposition") != "") {
						t.Error("denial served attachment")
					}
				})
			}
		}
	}
}

// Theme delivery has separate authority, but its existing byte helpers require
// absolute paths before local open/conversion. Do not assume checkSourceAccess
// covers this route; verify its actual signed-token path boundary as well.
func TestNativeContainmentThemeExistingPathBoundary(t *testing.T) {
	t.Chdir(t.TempDir())
	const key = "bloem-storage:malformed.mp3"
	const body = "unrelated-theme-local-bytes"
	if err := os.WriteFile(key, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(key)
	if err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		status     int
	}{{"reserved", key, 404}, {"physical-basename", absolute, 200}} {
		for _, method := range []string{"GET", "HEAD"} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				access := &fakeSourceAccess{}
				srv, _ := newSourceAccessProxyServer(t, access)
				srv.nodeRowID = func() (int, bool) { return 11, true }
				claims := themeDirectClaims(tc.path, stat.Size(), stat.ModTime().Truncate(time.Microsecond).UnixNano())
				token, err := streamtoken.Sign(claims, grantTestSecret, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				rr := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rr, httptest.NewRequest(method, "/stream/theme/"+token, nil))
				if rr.Code != tc.status {
					t.Errorf("theme=%d want=%d body=%q", rr.Code, tc.status, rr.Body.String())
				}
				if tc.status == 200 && method == "GET" && rr.Body.String() != body {
					t.Error("theme physical control bytes differ")
				}
				if tc.status == 404 && rr.Body.String() == body {
					t.Error("theme reserved local bytes escaped")
				}
				if access.calls != 0 {
					t.Error("theme test unexpectedly uses media-file authority")
				}
			})
		}
	}
	// A native key must also fail before invoking the local converter.
	srv, _ := newSourceAccessProxyServer(t, &fakeSourceAccess{})
	srv.nodeRowID = func() (int, bool) { return 11, true }
	token, err := streamtoken.Sign(themeAACClaims(key, stat.Size(), stat.ModTime().Truncate(time.Microsecond).UnixNano()), grantTestSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/stream/theme/"+token, nil))
	if rr.Code != 404 {
		t.Errorf("theme converter reached reserved identity: %d", rr.Code)
	}
}
