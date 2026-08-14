package compatcontract

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Direct-profile identity reference listener, mimicking the native surface's
// frozen semantics: a direct login binds one profile, the issued credential
// is captured and then proven bounded, and tokens outlived by a policy,
// security, or credential revision are refused. Observed over public HTTP.
// ---------------------------------------------------------------------------

const (
	refDirectEmail        = "direct-reader@fixture.example"
	refDirectPassword     = "fixture-direct-password-003"
	refDeviceID           = "fixture-device-tablet-01"
	refDirectProfileID    = "profile-direct-0000-0000-000000000001"
	refSiblingProfileID   = "profile-sibling-0000-0000-000000000002"
	refStalePolicyToken   = "fixture-stale-policy-token-101"
	refStaleSecurityToken = "fixture-stale-security-token-102"
	refResetCredToken     = "fixture-reset-credential-token-103"
)

func directProfileIdentityBindings() map[string]string {
	return map[string]string{
		"direct_email":           refDirectEmail,
		"direct_password":        refDirectPassword,
		"wrong_password":         refWrongPassword,
		"device_id":              refDeviceID,
		"direct_profile_id":      refDirectProfileID,
		"sibling_profile_id":     refSiblingProfileID,
		"direct_token":           "fixture-preseeded-direct-token",
		"stale_policy_token":     refStalePolicyToken,
		"stale_security_token":   refStaleSecurityToken,
		"reset_credential_token": refResetCredToken,
	}
}

// directProfileViolations seed exactly one contract breach each; the zero
// value is a compliant reference listener.
type directProfileViolations struct {
	discloseSiblingInLogin bool
	acceptWrongPassword    bool
	allowDirectory         bool
	allowHouseholdSessions bool
	allowSiblingUpdate     bool
	allowAPIKeyMint        bool
	allowPinVerify         bool
	hideOwnRecord          bool
	refuseOwnUpdate        bool
	acceptStalePolicy      bool
	acceptStaleSecurity    bool
	acceptResetCredential  bool
}

func newDirectProfileIdentityListener(t *testing.T, v directProfileViolations) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	nextToken := 0
	valid := map[string]bool{"fixture-preseeded-direct-token": true}
	authorized := func(r *http.Request) bool {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch token {
		case refStalePolicyToken:
			return v.acceptStalePolicy
		case refStaleSecurityToken:
			return v.acceptStaleSecurity
		case refResetCredToken:
			return v.acceptResetCredential
		}
		mu.Lock()
		defer mu.Unlock()
		return valid[token]
	}
	staleKind := func(r *http.Request) string {
		switch strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") {
		case refStalePolicyToken, refStaleSecurityToken:
			return "authorization_state_stale"
		}
		return "unauthorized"
	}
	refuse := func(w http.ResponseWriter, status int, code string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/profile-login", func(w http.ResponseWriter, r *http.Request) {
		var creds struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			DeviceID string `json:"device_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil || creds.DeviceID == "" {
			refuse(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if creds.Email != refDirectEmail || (creds.Password != refDirectPassword && !v.acceptWrongPassword) {
			refuse(w, http.StatusUnauthorized, "invalid_credentials")
			return
		}
		mu.Lock()
		nextToken++
		token := fmt.Sprintf("fixture-direct-session-%03d", nextToken)
		valid[token] = true
		mu.Unlock()
		payload := map[string]any{"access_token": token, "profile_id": refDirectProfileID}
		if v.discloseSiblingInLogin {
			payload["household_profiles"] = []string{refDirectProfileID, refSiblingProfileID}
		}
		writeFixtureJSON(w, payload)
	})
	requireAuth := func(next func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			if !authorized(r) {
				refuse(w, http.StatusUnauthorized, staleKind(r))
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/profiles/", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if v.allowDirectory {
			writeFixtureJSON(w, []map[string]any{{"id": refDirectProfileID}, {"id": refSiblingProfileID}})
			return
		}
		refuse(w, http.StatusForbidden, "direct_profile_scope")
	}))
	mux.HandleFunc("GET /api/v1/profiles/household/sessions", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if v.allowHouseholdSessions {
			writeFixtureJSON(w, []any{})
			return
		}
		refuse(w, http.StatusForbidden, "direct_profile_scope")
	}))
	mux.HandleFunc("PUT /api/v1/profiles/{id}", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == refDirectProfileID {
			if v.refuseOwnUpdate {
				refuse(w, http.StatusForbidden, "direct_profile_scope")
				return
			}
			writeFixtureJSON(w, map[string]any{"id": refDirectProfileID, "quality_preference": "720p"})
			return
		}
		if v.allowSiblingUpdate {
			writeFixtureJSON(w, map[string]any{"id": id, "name": "Hijacked"})
			return
		}
		refuse(w, http.StatusForbidden, "direct_profile_scope")
	}))
	mux.HandleFunc("GET /api/v1/profiles/{id}", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == refDirectProfileID && !v.hideOwnRecord {
			writeFixtureJSON(w, map[string]any{"id": refDirectProfileID, "name": "Reader"})
			return
		}
		refuse(w, http.StatusForbidden, "direct_profile_scope")
	}))
	mux.HandleFunc("POST /api/v1/profiles/{id}/verify-pin", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if v.allowPinVerify {
			writeFixtureJSON(w, map[string]any{"profile_token": "fixture-minted-profile-token-201"})
			return
		}
		refuse(w, http.StatusForbidden, "direct_profile_scope")
	}))
	mux.HandleFunc("POST /api/v1/api-keys/", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if v.allowAPIKeyMint {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"key": "fixture-minted-api-key-202"})
			return
		}
		refuse(w, http.StatusForbidden, "direct_profile_scope")
	}))
	mux.HandleFunc("GET /api/v1/settings/values", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		writeFixtureJSON(w, map[string]any{"values": map[string]any{}})
	}))
	return httptest.NewServer(mux)
}

func runDirectProfileReference(t *testing.T, v directProfileViolations, suite Suite) (Report, error) {
	t.Helper()
	server := newDirectProfileIdentityListener(t, v)
	defer server.Close()
	return Run(context.Background(), Target{
		BaseURL:  server.URL,
		Client:   server.Client(),
		Bindings: directProfileIdentityBindings(),
	}, suite)
}

func TestDirectProfileIdentityContractFreezesLeastPrivilege(t *testing.T) {
	report, err := runDirectProfileReference(t, directProfileViolations{}, DirectProfileIdentity())
	if err != nil {
		t.Fatalf("compliant listener: %v; report=%s", err, report.JSON())
	}

	for _, tt := range []struct {
		name       string
		violations directProfileViolations
		failing    string
	}{
		{"siblings disclosed at login", directProfileViolations{discloseSiblingInLogin: true}, "direct profile login binds exactly one profile"},
		{"wrong password accepted", directProfileViolations{acceptWrongPassword: true}, "a wrong profile password is refused"},
		{"profile directory served", directProfileViolations{allowDirectory: true}, "the profile directory is refused"},
		{"household sessions served", directProfileViolations{allowHouseholdSessions: true}, "household session management is refused"},
		{"sibling update allowed", directProfileViolations{allowSiblingUpdate: true}, "a sibling profile update is refused"},
		{"api key minted", directProfileViolations{allowAPIKeyMint: true}, "minting an account api key is refused"},
		{"pin verification allowed", directProfileViolations{allowPinVerify: true}, "verifying a profile pin is refused"},
		{"own record hidden", directProfileViolations{hideOwnRecord: true}, "the bound profile reads its own record"},
		{"own update refused", directProfileViolations{refuseOwnUpdate: true}, "the bound profile updates its own record"},
		{"stale policy accepted", directProfileViolations{acceptStalePolicy: true}, "a stale organization policy revision is refused"},
		{"stale security accepted", directProfileViolations{acceptStaleSecurity: true}, "a stale membership security revision is refused"},
		{"reset credential accepted", directProfileViolations{acceptResetCredential: true}, "a reset profile credential is refused"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report, err := runDirectProfileReference(t, tt.violations, DirectProfileIdentity())
			requireSingleFailure(t, report, err, tt.failing)
		})
	}
}
