package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/preview"
)

type previewTokenValidator interface {
	ValidateToken(token string) (*auth.Claims, error)
}

// previewHostAuthRoutes are the only auth routes served on the preview host:
// a guest refreshes and exits, it never signs in any other way.
var previewHostAuthRoutes = map[string]bool{
	"/api/v1/auth/refresh": true, "/api/v1/auth/logout": true,
	"/api/v2/auth/refresh": true, "/api/v2/auth/logout": true,
}

func previewHostRefusesPath(path string) bool {
	trimmed := strings.TrimSuffix(path, "/")
	switch {
	case previewHostAuthRoutes[trimmed]:
		return false
	case strings.HasPrefix(path, "/api/v1/auth/"), strings.HasPrefix(path, "/api/v2/auth/"),
		strings.HasPrefix(path, "/api/v2/password-resets"), trimmed == "/api/v2/capabilities/password-reset",
		strings.HasPrefix(path, "/api/v1/plugins/"), strings.HasPrefix(path, "/api/v1/plugin-assets/"),
		strings.HasPrefix(path, plugins.ContentPrefix+"/"):
		return true
	}
	return false
}

// previewHostGuard runs ahead of routing. Every request learns its Host (never
// X-Forwarded-Host) through auth.WithRequestHost. On the preview host, sign-in
// routes and plugin surfaces are refused and only guest credentials pass.
// A failed settings read refuses the request before any sign-in can run.
func previewHostGuard(settings auth.PreviewGuestSettingsSource, tokens previewTokenValidator) func(http.Handler) http.Handler {
	traffic := apimw.NewPreviewGuestLimiter()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := auth.HostOnly(r.Host)
			r = r.WithContext(auth.WithRequestHost(r.Context(), host))
			current, err := settings.PreviewGuestSettings(r.Context())
			carriesGuest := previewRequestCarriesGuest(r, tokens)
			guestRequest := (current.Host != "" && host == current.Host) || strings.HasPrefix(r.URL.Path, "/api/bloem/v1/preview/") || carriesGuest
			if guestRequest && !traffic.AllowGlobal() {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate_limited","message":"The preview is busy; try again shortly."}`))
				return
			}
			if guestRequest {
				r = r.WithContext(apimw.WithPreviewPrivacy(r.Context()))
				r.RemoteAddr = ""
				for _, header := range []string{"User-Agent", "X-Silo-Client", "X-Silo-Client-Version", "X-Silo-Client-Build", "X-Silo-Client-Family", "X-Silo-Client-Channel"} {
					r.Header.Del(header)
				}
			}
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":"unavailable","message":"Preview host configuration is unavailable."}`))
				return
			}
			if carriesGuest && host != current.Host {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized","code":"preview_session_invalid","message":"This preview session is not valid here."}`))
				return
			}
			if current.Host == "" || host != current.Host {
				next.ServeHTTP(w, r)
				return
			}
			if previewHostRefusesPath(r.URL.Path) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"forbidden","code":"preview_host","message":"This address only serves the preview."}`))
				return
			}
			if token := previewHostCredential(r); token != "" {
				claims, err := tokens.ValidateToken(token)
				if strings.HasPrefix(token, "sa_") || (err == nil && claims != nil && claims.AuthMethod != auth.AuthMethodPreviewGuest) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":"unauthorized","code":"preview_host","message":"Only preview sessions are accepted at this address."}`))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func previewHostCredential(r *http.Request) string {
	if header := r.Header.Get("Authorization"); len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return r.URL.Query().Get("token")
}

var (
	previewReadersMu sync.Mutex
	previewReaders   = map[*pgxpool.Pool]*preview.Reader{}
)

// bloemPreviewReader is the process's settings reader for one database, so the
// host guard, RequireAuth and the activity filter share one cache.
func bloemPreviewReader(deps Dependencies) *preview.Reader {
	if deps.DB == nil {
		return nil
	}
	previewReadersMu.Lock()
	defer previewReadersMu.Unlock()
	if reader := previewReaders[deps.DB]; reader != nil {
		return reader
	}
	reader := preview.NewReader(preview.NewStore(deps.DB), 5*time.Second)
	previewReaders[deps.DB] = reader
	return reader
}

func useBloemPreviewHost(r chi.Router, deps Dependencies) {
	reader := bloemPreviewReader(deps)
	if reader == nil || deps.Config == nil || deps.Config.Auth.JWTSecret == "" {
		return
	}
	r.Use(previewHostGuard(reader, previewHostTokens{JWTService: auth.NewJWTService(deps.Config.Auth.JWTSecret, time.Hour, time.Hour), privacyKey: []byte(deps.Config.Auth.JWTSecret)}))
	r.Use(previewAccountLoginGuard(deps.DB))
}

// Signature-only verification is used solely to suppress identifying logs,
// including for expired guest tokens. Authorization still validates expiry.
type previewHostTokens struct {
	*auth.JWTService
	privacyKey []byte
}

func (p previewHostTokens) previewGuestCredential(raw string) bool {
	claims := &auth.Claims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) { return p.privacyKey, nil }, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithoutClaimsValidation())
	return err == nil && token.Valid && claims.AuthMethod == auth.AuthMethodPreviewGuest
}

func previewRequestCarriesGuest(r *http.Request, tokens previewTokenValidator) bool {
	isGuest := func(raw string) bool {
		if raw == "" || tokens == nil {
			return false
		}
		if privacy, ok := tokens.(interface{ previewGuestCredential(string) bool }); ok {
			return privacy.previewGuestCredential(raw)
		}
		claims, err := tokens.ValidateToken(raw)
		return err == nil && claims != nil && claims.AuthMethod == auth.AuthMethodPreviewGuest
	}
	if isGuest(previewHostCredential(r)) {
		return true
	}
	if r.Method != http.MethodPost || (r.URL.Path != "/api/v1/auth/refresh" && r.URL.Path != "/api/v2/auth/refresh") || r.Body == nil {
		return false
	}
	original := r.Body
	var copied bytes.Buffer
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.NewDecoder(io.TeeReader(io.LimitReader(original, (1<<20)+1), &copied)).Decode(&body)
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(copied.Bytes()), original), original}
	return isGuest(body.RefreshToken)
}
