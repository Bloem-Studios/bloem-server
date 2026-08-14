package compatcontract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
)

func TestRunRejectsUnredactedSecrets(t *testing.T) {
	report, err := Run(context.Background(), Target{
		BaseURL: "http://127.0.0.1:8096?token=secret",
		Client:  http.DefaultClient,
	}, JellyfinBaseline())
	if err == nil || strings.Contains(report.JSON(), "secret") {
		t.Fatalf("err=%v report=%s", err, report.JSON())
	}
}

func TestRunChecksHTTPContracts(t *testing.T) {
	binary := []byte("fixture-bytes")
	checksum := sha256.Sum256(binary)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			w.Header().Set("X-Contract", "present")
			_, _ = w.Write([]byte(`{"items":[2,1],"name":"fixture"}`))
		case "/binary":
			_, _ = w.Write(binary)
		case "/denied":
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, Suite{
		Name: "http",
		Cases: []Case{
			{Name: "semantic json", Method: http.MethodGet, Path: "/json", WantStatus: http.StatusOK, WantHeaders: map[string]string{"X-Contract": "present"}, WantJSON: []byte(`{"name":"fixture","items":[2,1]}`)},
			{Name: "binary checksum", Method: http.MethodGet, Path: "/binary", WantStatus: http.StatusOK, WantSHA256: hex.EncodeToString(checksum[:])},
			{Name: "named unauthenticated exception", Method: http.MethodGet, Path: "/denied", Exception: ExceptionUnauthenticated},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v; report=%s", err, report.JSON())
	}
	if len(report.Results) != 3 {
		t.Fatalf("results = %d, want 3", len(report.Results))
	}
}

func TestRunChecksRequiredAndExcludedFixtureIdentifiers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":["ordinary-item-001"]}`))
	}))
	defer server.Close()

	_, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, Suite{
		Name: "visibility",
		Cases: []Case{{
			Name:           "ordinary catalog excludes adult item",
			Path:           "/items",
			WantStatus:     http.StatusOK,
			PresentStrings: []string{"ordinary-item-001"},
			AbsentStrings:  []string{"adult-item-001"},
		}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunPreservesFixtureQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") != "fixture" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	_, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, Suite{
		Name: "query",
		Cases: []Case{{
			Name:       "query path",
			Method:     http.MethodGet,
			Path:       "/resource?query=fixture",
			WantStatus: http.StatusNoContent,
		}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunChecksWebSocketMessages(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/socket" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade: %v", err)
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"MessageType":"KeepAlive"}`)); err != nil {
			t.Errorf("WriteMessage: %v", err)
		}
	}))
	defer server.Close()

	report, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, Suite{
		Name: "websocket",
		Cases: []Case{{
			Name:              "keepalive",
			Path:              "/socket",
			WantWebSocketJSON: []json.RawMessage{json.RawMessage(`{"MessageType":"KeepAlive"}`)},
		}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v; report=%s", err, report.JSON())
	}
}

// Deterministic fixture identities shared by both identity suites. Every
// value is invented, uses reserved domains, and appears verbatim in
// testdata/identity/*.json.
const (
	fixtureDirectEmail     = "direct-reader@fixture.example"
	fixtureDirectPassword  = "fixture-direct-password-001"
	fixtureAccountEmail    = "household@fixture.example"
	fixtureAccountPassword = "fixture-account-password-002"

	fixturePrimaryProfile  = "primary-profile-001"
	fixtureDirectProfile   = "direct-profile-002"
	fixturePinProfile      = "pin-profile-003"
	fixtureExcludedProfile = "excluded-profile-004"
	fixtureForeignItem     = "foreign-item-901"
	fixtureOrdinaryItem    = "ordinary-item-001"
	fixtureAdultItem       = "adult-item-001"
	fixtureProfilePIN      = "246810"

	fixtureAccountToken = "fixture-account-session-token-201"
	fixtureDirectToken  = "fixture-direct-session-token-202"
	fixtureIssuedToken  = "fixture-issued-session-token-301"
)

// fixtureStaleTokens are session tokens minted before each revocation
// revision; a conforming listener refuses every one of them.
var fixtureStaleTokens = []string{
	"fixture-stale-security-revision-token-101",
	"fixture-stale-credential-revision-token-102",
	"fixture-stale-membership-revision-token-103",
	"fixture-stale-account-policy-revision-token-104",
	"fixture-stale-org-policy-revision-token-105",
	"fixture-revoked-device-token-106",
}

// identityRevocationCaseNames freeze that every revocation revision has its
// own named case in both identity suites.
var identityRevocationCaseNames = []string{
	"a stale security revision is refused",
	"a reset credential revision is refused",
	"a stale membership revision is refused",
	"a stale account policy revision is refused",
	"a stale organization policy revision is refused",
	"a revoked device is refused",
}

func writeFixtureJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// jellyfinViolations switch individual contract breaches on so the identity
// suite can be proven to detect each of them. The zero value is a compliant
// reference listener.
type jellyfinViolations struct {
	discloseDirectory     bool
	discloseSiblings      bool
	discloseUntrustedTile bool
	grantAdmin            bool
	skipPIN               bool
	acceptStaleTokens     bool
	discloseForeignItem   bool
	discloseAdultCatalog  bool
	discloseAdultArtwork  bool
	discloseAdultPlayback bool
	leakAdultEvent        bool
}

// newJellyfinIdentityListener serves the frozen Jellyfin identity semantics
// over public HTTP: every behavior the suite pins is observable only through
// requests and responses, never through store internals.
func newJellyfinIdentityListener(t *testing.T, v jellyfinViolations) *httptest.Server {
	t.Helper()
	stale := map[string]bool{}
	for _, token := range fixtureStaleTokens {
		stale[token] = true
	}
	authorized := func(r *http.Request) bool {
		token := r.Header.Get("X-Emby-Token")
		if token == fixtureAccountToken || token == fixtureDirectToken {
			return true
		}
		return stale[token] && v.acceptStaleTokens
	}
	refuse := func(w http.ResponseWriter) { http.Error(w, "unauthorized", http.StatusUnauthorized) }
	writeLogin := func(w http.ResponseWriter, profileID string) {
		payload := map[string]any{
			"User":        map[string]any{"Id": profileID, "Name": profileID, "Policy": map[string]any{"IsAdministrator": v.grantAdmin}},
			"AccessToken": fixtureIssuedToken,
		}
		if v.discloseSiblings {
			payload["HouseholdProfiles"] = []string{fixturePrimaryProfile, fixturePinProfile}
		}
		writeFixtureJSON(w, payload)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		var creds struct{ Username, Pw string }
		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		trustedDevice := r.Header.Get("X-Emby-Token") == fixtureAccountToken
		switch {
		case creds.Username == fixtureDirectEmail && creds.Pw == fixtureDirectPassword:
			writeLogin(w, fixtureDirectProfile)
		case creds.Username == fixtureAccountEmail && creds.Pw == fixtureAccountPassword:
			writeLogin(w, fixturePrimaryProfile)
		case creds.Username == fixturePrimaryProfile && creds.Pw == "" && trustedDevice:
			writeLogin(w, fixturePrimaryProfile)
		case creds.Username == fixturePinProfile && trustedDevice && (creds.Pw == fixtureProfilePIN || (creds.Pw == "" && v.skipPIN)):
			writeLogin(w, fixturePinProfile)
		default:
			refuse(w)
		}
	})
	mux.HandleFunc("GET /Users/Public", func(w http.ResponseWriter, _ *http.Request) {
		if v.discloseDirectory {
			writeFixtureJSON(w, []map[string]any{{"Id": fixturePrimaryProfile}, {"Id": fixtureDirectProfile}, {"Id": fixturePinProfile}})
			return
		}
		writeFixtureJSON(w, []any{})
	})
	mux.HandleFunc("GET /Users", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			refuse(w)
			return
		}
		if r.Header.Get("X-Emby-Token") == fixtureDirectToken && !v.discloseSiblings {
			writeFixtureJSON(w, []map[string]any{{"Id": fixtureDirectProfile}})
			return
		}
		tiles := []map[string]any{{"Id": fixturePrimaryProfile}, {"Id": fixtureDirectProfile}, {"Id": fixturePinProfile}}
		if v.discloseUntrustedTile {
			tiles = append(tiles, map[string]any{"Id": fixtureExcludedProfile})
		}
		writeFixtureJSON(w, tiles)
	})
	mux.HandleFunc("GET /Sessions", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			refuse(w)
			return
		}
		writeFixtureJSON(w, []any{})
	})
	mux.HandleFunc("GET /Items", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			refuse(w)
			return
		}
		items := []map[string]any{{"Id": fixtureOrdinaryItem, "Name": "Ordinary title"}}
		if v.discloseAdultCatalog {
			items = append(items, map[string]any{"Id": fixtureAdultItem, "Name": "Adult title"})
		}
		writeFixtureJSON(w, map[string]any{"Items": items, "TotalRecordCount": len(items)})
	})
	mux.HandleFunc("GET /Items/{id}", func(w http.ResponseWriter, r *http.Request) {
		if v.discloseForeignItem && r.PathValue("id") == fixtureForeignItem {
			writeFixtureJSON(w, map[string]any{"Id": fixtureForeignItem})
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /Items/{id}/PlaybackInfo", func(w http.ResponseWriter, r *http.Request) {
		if v.discloseAdultPlayback && r.PathValue("id") == fixtureAdultItem {
			writeFixtureJSON(w, map[string]any{"MediaSources": []map[string]any{{"Id": fixtureAdultItem}}})
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /Items/{id}/Images/Primary", func(w http.ResponseWriter, r *http.Request) {
		if v.discloseAdultArtwork && r.PathValue("id") == fixtureAdultItem {
			_, _ = w.Write([]byte("adult-image-001"))
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /Videos/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		if v.discloseAdultPlayback && r.PathValue("id") == fixtureAdultItem {
			_, _ = w.Write([]byte(fixtureAdultItem))
			return
		}
		http.NotFound(w, r)
	})
	upgrader := websocket.Upgrader{}
	mux.HandleFunc("GET /socket", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if v.leakAdultEvent {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"MessageType":"LibraryChanged","Data":{"ItemsAdded":["adult-item-001"]}}`))
		}
	})
	return httptest.NewServer(mux)
}

func TestUnknownDeviceNeverReceivesProfileDirectory(t *testing.T) {
	c := UnknownJellyfinDeviceProfileList()
	if count, ok := c.WantJSONCounts["$"]; !ok || count != 0 {
		t.Fatalf("case pins WantJSONCounts[$] = %d (present %t), want an empty directory count", count, ok)
	}
	suite := Suite{Name: "jellyfin-unknown-device", Cases: []Case{c}}

	compliant := newJellyfinIdentityListener(t, jellyfinViolations{})
	defer compliant.Close()
	report, err := Run(context.Background(), Target{BaseURL: compliant.URL, Client: compliant.Client()}, suite)
	if err != nil {
		t.Fatalf("compliant listener: %v; report=%s", err, report.JSON())
	}

	disclosing := newJellyfinIdentityListener(t, jellyfinViolations{discloseDirectory: true})
	defer disclosing.Close()
	if _, err := Run(context.Background(), Target{BaseURL: disclosing.URL, Client: disclosing.Client()}, suite); err == nil {
		t.Fatal("a disclosed public profile directory passed the contract")
	}
}

func TestJellyfinIdentityContractFreezesSubjectSemantics(t *testing.T) {
	compliant := newJellyfinIdentityListener(t, jellyfinViolations{})
	defer compliant.Close()
	report, err := Run(context.Background(), Target{BaseURL: compliant.URL, Client: compliant.Client()}, JellyfinIdentity())
	if err != nil {
		t.Fatalf("compliant listener: %v; report=%s", err, report.JSON())
	}

	for _, tt := range []struct {
		name       string
		violations jellyfinViolations
		failing    string
	}{
		{"disclosed public directory", jellyfinViolations{discloseDirectory: true}, "unknown device receives no profile directory"},
		{"direct login disclosing siblings", jellyfinViolations{discloseSiblings: true}, "direct profile login binds exactly one profile"},
		{"direct login granted administration", jellyfinViolations{grantAdmin: true}, "direct profile login binds exactly one profile"},
		{"untrusted profile tile", jellyfinViolations{discloseUntrustedTile: true}, "trusted device lists only authorized profile tiles"},
		{"pin switch without the pin", jellyfinViolations{skipPIN: true}, "pin protected profile refuses a switch without its pin"},
		{"stale revisions accepted", jellyfinViolations{acceptStaleTokens: true}, "a stale security revision is refused"},
		{"cross organization disclosure", jellyfinViolations{discloseForeignItem: true}, "cross organization item is not disclosed"},
		{"adult item in catalog count", jellyfinViolations{discloseAdultCatalog: true}, "ordinary catalog count excludes adult items"},
		{"adult artwork served", jellyfinViolations{discloseAdultArtwork: true}, "adult artwork is not disclosed"},
		{"adult playback negotiated", jellyfinViolations{discloseAdultPlayback: true}, "adult playback negotiation is not disclosed"},
		{"adult event emitted", jellyfinViolations{leakAdultEvent: true}, "events omit adult items"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newJellyfinIdentityListener(t, tt.violations)
			defer server.Close()
			_, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, JellyfinIdentity())
			if err == nil {
				t.Fatal("a violating listener passed the identity contract")
			}
			if !strings.Contains(err.Error(), tt.failing) {
				t.Fatalf("failure = %v, want it to name %q", err, tt.failing)
			}
		})
	}
}

func TestRunChecksJSONCounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/object":
			_, _ = w.Write([]byte(`{"Items":[1,2],"Nested":{"Rows":["only"]},"Scalar":7}`))
		case "/array":
			_, _ = w.Write([]byte(`[1,2,3]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	run := func(c Case) error {
		_, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, Suite{Name: "counts", Cases: []Case{c}})
		return err
	}

	if err := run(Case{Name: "matching counts", Path: "/object", WantStatus: http.StatusOK, WantJSONCounts: map[string]int{"$.Items": 2, "$.Nested.Rows": 1}}); err != nil {
		t.Fatalf("matching counts: %v", err)
	}
	if err := run(Case{Name: "root array", Path: "/array", WantStatus: http.StatusOK, WantJSONCounts: map[string]int{"$": 3}}); err != nil {
		t.Fatalf("root array: %v", err)
	}
	if err := run(Case{Name: "count mismatch", Path: "/object", WantJSONCounts: map[string]int{"$.Items": 0}}); err == nil {
		t.Fatal("a wrong element count passed")
	}
	if err := run(Case{Name: "absent selector", Path: "/object", WantJSONCounts: map[string]int{"$.Missing": 0}}); err == nil {
		t.Fatal("an absent selector passed as an empty collection")
	}
	if err := run(Case{Name: "non-array selector", Path: "/object", WantJSONCounts: map[string]int{"$.Scalar": 1}}); err == nil {
		t.Fatal("a non-array selector passed")
	}
}

func TestRunTimingSamplesCarryCaseHeaders(t *testing.T) {
	var total, authed atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		total.Add(1)
		if r.Header.Get("X-Fixture-Auth") == "fixture-auth-901" {
			authed.Add(1)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	_, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, Suite{
		Name: "timing-headers",
		Cases: []Case{{
			Name:      "timing samples reuse the case identity",
			Path:      "/missing-adult-item-001",
			Headers:   map[string]string{"X-Fixture-Auth": "fixture-auth-901"},
			Exception: ExceptionNotFound,
			Timing: &TimingDistribution{
				ControlPath: "/missing-random-item-002",
				Samples:     4,
				MaxRatio:    20,
			},
		}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if total.Load() == 0 || authed.Load() != total.Load() {
		t.Fatalf("authenticated requests = %d of %d, want every timing sample to carry the case headers", authed.Load(), total.Load())
	}
}

func TestRunBoundsNegativeTimingDistribution(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	_, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, Suite{
		Name: "timing",
		Cases: []Case{{
			Name:       "adult and random missing IDs are indistinguishable",
			Method:     http.MethodGet,
			Path:       "/missing-adult-item-001",
			WantStatus: http.StatusNotFound,
			Timing: &TimingDistribution{
				ControlPath: "/missing-random-item-002",
				Samples:     3,
				MaxRatio:    20,
			},
		}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}
