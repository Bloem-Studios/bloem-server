package compatcontract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// ---------------------------------------------------------------------------
// Runner mechanism tests: bindings, captures, semantic fields, timing, and
// filtered websocket watching.
// ---------------------------------------------------------------------------

func TestRunCapturesAndChainsBindings(t *testing.T) {
	const issued = "fixture-issued-credential-0001"
	var seen atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			writeFixtureJSON(w, map[string]any{"token": issued, "profile": "Reader-Nook"})
		case "/whoami":
			seen.Store(r.Header.Get("X-Fixture-Auth"))
			if r.Header.Get("X-Fixture-Auth") != issued {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			writeFixtureJSON(w, map[string]any{"profile": "Reader-Nook"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	bindings := map[string]string{}
	target := Target{BaseURL: server.URL, Client: server.Client(), Bindings: bindings}
	if _, err := Run(context.Background(), target, Suite{
		Name: "chain",
		Cases: []Case{
			{Name: "login captures the issued credential", Method: http.MethodPost, Path: "/login", WantStatus: http.StatusOK, Capture: map[string]string{"chained_token": "$.token"}},
			{Name: "the issued credential is spent by the next case", Path: "/whoami", Headers: map[string]string{"X-Fixture-Auth": "{{chained_token}}"}, WantStatus: http.StatusOK, WantJSONFields: map[string]json.RawMessage{"$.profile": json.RawMessage(`"Reader-Nook"`)}},
		},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, _ := seen.Load().(string); got != issued {
		t.Fatalf("server saw credential %q, want the captured %q", got, issued)
	}
	if bindings["chained_token"] != issued {
		t.Fatalf("bindings retain %q, want the captured credential for cross-Run chaining", bindings["chained_token"])
	}

	// A second Run against the same bindings map spends the capture from the
	// first, which is how consumers chain phases around state changes.
	if _, err := Run(context.Background(), target, Suite{
		Name:  "chain-phase-two",
		Cases: []Case{{Name: "the captured credential survives across runs", Path: "/whoami", Headers: map[string]string{"X-Fixture-Auth": "{{chained_token}}"}, WantStatus: http.StatusOK}},
	}); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
}

func TestRunFailsOnUnboundPlaceholder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client(), Bindings: map[string]string{}}, Suite{
		Name:  "unbound",
		Cases: []Case{{Name: "unbound placeholder", Path: "/{{never_bound}}", WantStatus: http.StatusOK}},
	})
	if err == nil || !strings.Contains(err.Error(), "never_bound") {
		t.Fatalf("Run() error = %v, want an unbound-placeholder failure naming the binding", err)
	}
}

func TestRunChecksSemanticJSONFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The body deliberately contains the byte substring a naive check
		// would search for, while the real field is false — and the real
		// field is formatted with whitespace a substring probe would miss.
		_, _ = w.Write([]byte(`{"Doc": "\"IsAdministrator\":true", "Policy": {"IsAdministrator": false}}`))
	}))
	defer server.Close()

	run := func(c Case) error {
		_, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, Suite{Name: "fields", Cases: []Case{c}})
		return err
	}

	if err := run(Case{Name: "semantic false", Path: "/", WantStatus: http.StatusOK, WantJSONFields: map[string]json.RawMessage{"$.Policy.IsAdministrator": json.RawMessage(`false`)}}); err != nil {
		t.Fatalf("semantic field assertion: %v", err)
	}
	if err := run(Case{Name: "semantic mismatch", Path: "/", WantJSONFields: map[string]json.RawMessage{"$.Policy.IsAdministrator": json.RawMessage(`true`)}}); err == nil {
		t.Fatal("a mismatched field value passed")
	}
	if err := run(Case{Name: "missing field", Path: "/", WantJSONFields: map[string]json.RawMessage{"$.Policy.Missing": json.RawMessage(`false`)}}); err == nil {
		t.Fatal("an absent field passed as matching")
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

// A control path that answers a different status is itself a disclosure
// oracle; timing must refuse it rather than comparing durations of visibly
// different responses.
func TestRunTimingRejectsUnequalControlStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing-random-item-002" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	_, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, Suite{
		Name: "timing-status",
		Cases: []Case{{
			Name:      "control status differs",
			Path:      "/missing-adult-item-001",
			Exception: ExceptionNotFound,
			Timing:    &TimingDistribution{ControlPath: "/missing-random-item-002", Samples: 3, MaxRatio: 20},
		}},
	})
	if err == nil {
		t.Fatal("a control path answering a different status passed the timing contract")
	}
}

// A stable delay on only the protected path is a measurable oracle even when
// it sits under any fixed absolute allowance; the median comparison must
// reject it.
func TestRunTimingRejectsStableProtectedDelay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing-adult-item-001" {
			time.Sleep(12 * time.Millisecond)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	_, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, Suite{
		Name: "timing-delay",
		Cases: []Case{{
			Name:      "stable protected delay",
			Path:      "/missing-adult-item-001",
			Exception: ExceptionNotFound,
			Timing:    &TimingDistribution{ControlPath: "/missing-random-item-002", Samples: 8, MaxRatio: 3},
		}},
	})
	if err == nil {
		t.Fatal("a stable 12ms delay on the protected path passed the timing contract")
	}
}

// The filtered websocket watch accepts legitimate traffic — keepalives and
// ordinary events — and fails only on frames carrying excluded identifiers.
func TestRunWatchesWebSocketTrafficForExclusions(t *testing.T) {
	upgrader := websocket.Upgrader{}
	newServer := func(leakAdult bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"MessageType":"KeepAlive"}`))
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"MessageType":"LibraryChanged","Data":{"ItemsAdded":["ordinary-item-001"]}}`))
			if leakAdult {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"MessageType":"LibraryChanged","Data":{"ItemsAdded":["adult-item-001"]}}`))
			}
			time.Sleep(200 * time.Millisecond)
		}))
	}
	suite := Suite{Name: "socket-watch", Cases: []Case{{
		Name:                  "events omit adult items",
		Path:                  "/socket",
		WantWebSocketFiltered: true,
		AbsentStrings:         []string{"adult-item-001"},
	}}}

	benign := newServer(false)
	defer benign.Close()
	if _, err := Run(context.Background(), Target{BaseURL: benign.URL, Client: benign.Client()}, suite); err != nil {
		t.Fatalf("legitimate keepalive and event traffic failed the watch: %v", err)
	}

	leaking := newServer(true)
	defer leaking.Close()
	if _, err := Run(context.Background(), Target{BaseURL: leaking.URL, Client: leaking.Client()}, suite); err == nil {
		t.Fatal("an adult event frame passed the filtered watch")
	}
}

// ---------------------------------------------------------------------------
// Jellyfin identity reference listener. It mimics the embedded router's real
// login conventions and response shapes so the same fixture document that
// runs against the real router can be proven, violation by violation, to
// detect each breach. Everything is observed over public HTTP.
// ---------------------------------------------------------------------------

// Deterministic fixture identities shared by the reference listeners.
const (
	refAccountUsername = "casa-account"
	refAccountPassword = "fixture-account-password-001"
	refWrongPassword   = "fixture-wrong-password-002"
	refPrimaryProfile  = "Primary-Casa"
	refReaderProfile   = "Reader-Nook"
	refPinProfile      = "Vault-Keep"
	refUnknownProfile  = "Ghost-Shelf"
	refForeignProfile  = "Foreign-Family"
	refProfilePIN      = "246810"
	refWrongPIN        = "135791"
	refAccountID       = "31"
	refMissingAdultID  = "missing-adult-item-001"
	refMissingRandomID = "missing-random-item-002"
)

func writeFixtureJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// jellyfinIdentityBindings returns a fresh bindings map for the reference
// listener. Session tokens are pre-seeded with valid sessions so a mutation
// that fails one login case cannot cascade into the cases that spend the
// captured token: on a compliant run the captures overwrite these values.
func jellyfinIdentityBindings() map[string]string {
	return map[string]string{
		"account_username":  refAccountUsername,
		"account_password":  refAccountPassword,
		"wrong_password":    refWrongPassword,
		"primary_profile":   refPrimaryProfile,
		"reader_profile":    refReaderProfile,
		"pin_profile":       refPinProfile,
		"unknown_profile":   refUnknownProfile,
		"foreign_profile":   refForeignProfile,
		"profile_pin":       refProfilePIN,
		"wrong_pin":         refWrongPIN,
		"sibling_pseudo_id": "pseudo-" + refPrimaryProfile,
		"foreign_pseudo_id": "pseudo-" + refForeignProfile,
		"missing_adult_id":  refMissingAdultID,
		"missing_random_id": refMissingRandomID,
		"jf_account_token":  "fixture-preseeded-account-token",
		"jf_reader_token":   "fixture-preseeded-reader-token",
		"jf_pin_token":      "fixture-preseeded-pin-token",
		"jf_revoked_token":  "fixture-revoked-compat-token-903",
	}
}

// jellyfinViolations seed exactly one contract breach each; the zero value is
// a compliant reference listener.
type jellyfinViolations struct {
	discloseDirectory       bool
	acceptWrongPassword     bool
	misrememberProfile      bool
	adminOnExplicitLogin    bool
	allowPinlessSwitch      bool
	acceptWrongPin          bool
	refusePinLogin          bool
	acceptUnknownProfile    bool
	discloseSiblingTiles    bool
	discloseSiblingUser     bool
	discloseForeignUser     bool
	misattributeCurrentUser bool
	logoutWrongStatus       bool
	ignoreLogout            bool
	acceptRevokedToken      bool
	leakAdultEvent          bool
	delayAdultMiss          bool
	unequalControlStatus    bool
}

type jellyfinRefSession struct {
	combined string
	profile  string
}

func newJellyfinIdentityListener(t *testing.T, v jellyfinViolations) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	nextToken := 0
	sessions := map[string]jellyfinRefSession{
		"fixture-preseeded-account-token": {combined: refAccountUsername, profile: refPrimaryProfile},
		"fixture-preseeded-reader-token":  {combined: refAccountUsername + "#" + refReaderProfile, profile: refReaderProfile},
		"fixture-preseeded-pin-token":     {combined: refAccountUsername + "#" + refPinProfile, profile: refPinProfile},
	}
	if v.acceptRevokedToken {
		sessions["fixture-revoked-compat-token-903"] = jellyfinRefSession{combined: refAccountUsername, profile: refPrimaryProfile}
	}
	sessionFor := func(r *http.Request) (jellyfinRefSession, bool) {
		mu.Lock()
		defer mu.Unlock()
		session, ok := sessions[r.Header.Get("X-Emby-Token")]
		return session, ok
	}
	refuse := func(w http.ResponseWriter) { http.Error(w, "unauthorized", http.StatusUnauthorized) }
	userDTO := func(session jellyfinRefSession, admin bool) map[string]any {
		return map[string]any{
			"Id":     "pseudo-" + session.profile,
			"Name":   session.combined,
			"Policy": map[string]any{"IsAdministrator": admin},
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		var creds struct{ Username, Pw string }
		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		account, profileSelector := creds.Username, ""
		if i := strings.LastIndexByte(creds.Username, '#'); i >= 0 {
			account, profileSelector = creds.Username[:i], creds.Username[i+1:]
		}
		password, pin := creds.Pw, ""
		if i := strings.LastIndexByte(creds.Pw, '#'); i > 0 {
			password, pin = creds.Pw[:i], creds.Pw[i+1:]
		}
		passwordOK := creds.Pw == refAccountPassword || password == refAccountPassword
		if creds.Pw == refAccountPassword {
			pin = ""
		}
		if account != refAccountUsername || (!passwordOK && !v.acceptWrongPassword) {
			refuse(w)
			return
		}
		profile := refPrimaryProfile
		if v.misrememberProfile {
			profile = refReaderProfile
		}
		if profileSelector != "" {
			switch profileSelector {
			case refPrimaryProfile, refReaderProfile, refPinProfile:
				profile = profileSelector
			default:
				if !v.acceptUnknownProfile {
					refuse(w)
					return
				}
				profile = profileSelector
			}
		}
		if profile == refPinProfile {
			if v.refusePinLogin {
				refuse(w)
				return
			}
			pinOK := pin == refProfilePIN || (pin == "" && v.allowPinlessSwitch) || (pin != "" && v.acceptWrongPin)
			if !pinOK {
				refuse(w)
				return
			}
		}
		admin := v.adminOnExplicitLogin && profileSelector == refReaderProfile
		mu.Lock()
		nextToken++
		token := fmt.Sprintf("fixture-compat-session-%03d", nextToken)
		session := jellyfinRefSession{combined: creds.Username, profile: profile}
		sessions[token] = session
		mu.Unlock()
		writeFixtureJSON(w, map[string]any{
			"AccessToken": token,
			"ServerId":    "fixture-server",
			"User":        userDTO(session, admin),
			"SessionInfo": map[string]any{"UserId": "pseudo-" + profile, "UserName": profile},
		})
	})
	mux.HandleFunc("GET /Users/Public", func(w http.ResponseWriter, _ *http.Request) {
		if v.discloseDirectory {
			writeFixtureJSON(w, []map[string]any{{"Name": refPrimaryProfile}, {"Name": refReaderProfile}, {"Name": refPinProfile}})
			return
		}
		writeFixtureJSON(w, []any{})
	})
	mux.HandleFunc("GET /Users", func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFor(r)
		if !ok {
			refuse(w)
			return
		}
		tiles := []map[string]any{userDTO(session, false)}
		if v.discloseSiblingTiles {
			tiles = append(tiles, userDTO(jellyfinRefSession{combined: refAccountUsername + "#" + refPinProfile, profile: refPinProfile}, false))
		}
		writeFixtureJSON(w, tiles)
	})
	mux.HandleFunc("GET /Users/Me", func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFor(r)
		if !ok {
			refuse(w)
			return
		}
		if v.misattributeCurrentUser {
			session = jellyfinRefSession{combined: refAccountUsername + "#" + refPrimaryProfile, profile: refPrimaryProfile}
		}
		writeFixtureJSON(w, userDTO(session, false))
	})
	mux.HandleFunc("GET /Users/{id}", func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFor(r)
		if !ok {
			refuse(w)
			return
		}
		requested := r.PathValue("id")
		switch {
		case requested == "pseudo-"+session.profile:
			writeFixtureJSON(w, userDTO(session, false))
		case v.discloseSiblingUser && requested == "pseudo-"+refPrimaryProfile:
			writeFixtureJSON(w, map[string]any{"Id": requested, "Name": refPrimaryProfile})
		case v.discloseForeignUser && requested == "pseudo-"+refForeignProfile:
			writeFixtureJSON(w, map[string]any{"Id": requested, "Name": refForeignProfile})
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("POST /Sessions/Logout", func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Emby-Token")
		mu.Lock()
		_, ok := sessions[token]
		if ok && !v.ignoreLogout {
			delete(sessions, token)
		}
		mu.Unlock()
		if !ok {
			refuse(w)
			return
		}
		if v.logoutWrongStatus {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	upgrader := websocket.Upgrader{}
	mux.HandleFunc("GET /socket", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := sessionFor(r); !ok {
			refuse(w)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		// Legitimate traffic: a keepalive and an ordinary event.
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"MessageType":"KeepAlive"}`))
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"MessageType":"LibraryChanged","Data":{"ItemsAdded":["ordinary-item-001"]}}`))
		if v.leakAdultEvent {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"MessageType":"LibraryChanged","Data":{"ItemsAdded":["adult-item-001"]}}`))
		}
		time.Sleep(200 * time.Millisecond)
	})
	mux.HandleFunc("GET /Items/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := sessionFor(r); !ok {
			refuse(w)
			return
		}
		id := r.PathValue("id")
		if v.delayAdultMiss && id == refMissingAdultID {
			time.Sleep(12 * time.Millisecond)
		}
		if v.unequalControlStatus && id == refMissingRandomID {
			refuse(w)
			return
		}
		http.NotFound(w, r)
	})
	return httptest.NewServer(mux)
}

func runJellyfinReference(t *testing.T, v jellyfinViolations, suite Suite) (Report, error) {
	t.Helper()
	server := newJellyfinIdentityListener(t, v)
	defer server.Close()
	return Run(context.Background(), Target{
		BaseURL:  server.URL,
		Client:   server.Client(),
		Bindings: jellyfinIdentityBindings(),
	}, suite)
}

// requireSingleFailure asserts a seeded violation fails exactly the case that
// owns the behavior: one failed result, carrying the expected name.
func requireSingleFailure(t *testing.T, report Report, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatal("a violating listener passed the identity contract")
	}
	failed := report.FailedCases()
	if len(failed) != 1 || failed[0] != want {
		t.Fatalf("failed cases = %v, want exactly [%q]; report=%s", failed, want, report.JSON())
	}
}

func TestUnknownDeviceNeverReceivesProfileDirectory(t *testing.T) {
	c := UnknownJellyfinDeviceProfileList()
	if count, ok := c.WantJSONCounts["$"]; !ok || count != 0 {
		t.Fatalf("case pins WantJSONCounts[$] = %d (present %t), want an empty directory count", count, ok)
	}
	suite := Suite{Name: "jellyfin-unknown-device", Cases: []Case{c}}

	if report, err := runJellyfinReference(t, jellyfinViolations{}, suite); err != nil {
		t.Fatalf("compliant listener: %v; report=%s", err, report.JSON())
	}
	report, err := runJellyfinReference(t, jellyfinViolations{discloseDirectory: true}, suite)
	requireSingleFailure(t, report, err, c.Name)
}

func TestJellyfinIdentityContractFreezesSubjectSemantics(t *testing.T) {
	report, err := runJellyfinReference(t, jellyfinViolations{}, JellyfinIdentity())
	if err != nil {
		t.Fatalf("compliant listener: %v; report=%s", err, report.JSON())
	}

	for _, tt := range []struct {
		name       string
		violations jellyfinViolations
		failing    string
	}{
		{"disclosed public directory", jellyfinViolations{discloseDirectory: true}, "unknown device receives no profile directory"},
		{"wrong password accepted", jellyfinViolations{acceptWrongPassword: true}, "a wrong password is refused"},
		{"wrong remembered profile", jellyfinViolations{misrememberProfile: true}, "legacy account login selects the remembered profile"},
		{"administrator granted on login", jellyfinViolations{adminOnExplicitLogin: true}, "an explicit profile selection needs no pin"},
		{"pinless switch allowed", jellyfinViolations{allowPinlessSwitch: true}, "a pin protected profile refuses a switch without its pin"},
		{"wrong pin accepted", jellyfinViolations{acceptWrongPin: true}, "a wrong pin is refused"},
		{"correct pin refused", jellyfinViolations{refusePinLogin: true}, "a pin protected profile switches with its pin"},
		{"unknown profile accepted", jellyfinViolations{acceptUnknownProfile: true}, "an unknown profile is refused"},
		{"sibling tiles disclosed", jellyfinViolations{discloseSiblingTiles: true}, "the session sees itself and nothing more"},
		{"sibling user disclosed", jellyfinViolations{discloseSiblingUser: true}, "a sibling user id is not disclosed"},
		{"foreign user disclosed", jellyfinViolations{discloseForeignUser: true}, "another household's user id is not disclosed"},
		{"current user misattributed", jellyfinViolations{misattributeCurrentUser: true}, "the current user endpoint answers the bound session"},
		{"logout answers the wrong status", jellyfinViolations{logoutWrongStatus: true}, "logging out revokes the compat session"},
		{"logout ignored", jellyfinViolations{ignoreLogout: true}, "a logged out session is refused"},
		{"revoked session accepted", jellyfinViolations{acceptRevokedToken: true}, "a revoked account session is refused"},
		{"adult event leaked", jellyfinViolations{leakAdultEvent: true}, "events omit adult items"},
		{"stable adult delay", jellyfinViolations{delayAdultMiss: true}, "missing adult and missing random ids time alike"},
		{"control status differs", jellyfinViolations{unequalControlStatus: true}, "missing adult and missing random ids time alike"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report, err := runJellyfinReference(t, tt.violations, JellyfinIdentity())
			requireSingleFailure(t, report, err, tt.failing)
		})
	}
}
