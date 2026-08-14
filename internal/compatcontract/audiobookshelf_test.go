package compatcontract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

const fixtureOrdinaryLibrary = "lib-ordinary-001"

// audiobookshelfViolations switch individual contract breaches on so the
// identity suite can be proven to detect each of them. The zero value is a
// compliant reference listener.
type audiobookshelfViolations struct {
	discloseDirectory     bool
	discloseSiblings      bool
	grantRoot             bool
	acceptStaleTokens     bool
	discloseForeignItem   bool
	discloseAdultCatalog  bool
	discloseAdultArtwork  bool
	discloseAdultPlayback bool
	leakAdultActivity     bool
}

// newAudiobookshelfIdentityListener serves the frozen Audiobookshelf identity
// semantics over public HTTP; the suite observes nothing but requests and
// responses.
func newAudiobookshelfIdentityListener(t *testing.T, v audiobookshelfViolations) *httptest.Server {
	t.Helper()
	stale := map[string]bool{}
	for _, token := range fixtureStaleTokens {
		stale[token] = true
	}
	authorized := func(r *http.Request) bool {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == fixtureAccountToken || token == fixtureDirectToken {
			return true
		}
		return stale[token] && v.acceptStaleTokens
	}
	refuse := func(w http.ResponseWriter) { http.Error(w, "unauthorized", http.StatusUnauthorized) }
	writeLogin := func(w http.ResponseWriter, profileID string) {
		userType := "user"
		if v.grantRoot {
			userType = "root"
		}
		user := map[string]any{"id": profileID, "type": userType}
		if v.discloseSiblings {
			user["householdProfiles"] = []string{fixturePrimaryProfile, fixturePinProfile}
		}
		writeFixtureJSON(w, map[string]any{"user": user, "userDefaultLibraryId": fixtureOrdinaryLibrary})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		var creds struct{ Username, Password string }
		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		switch {
		case creds.Username == fixtureDirectEmail && creds.Password == fixtureDirectPassword:
			writeLogin(w, fixtureDirectProfile)
		case creds.Username == fixtureAccountEmail && creds.Password == fixtureAccountPassword:
			writeLogin(w, fixturePrimaryProfile)
		default:
			refuse(w)
		}
	})
	mux.HandleFunc("GET /api/users", func(w http.ResponseWriter, r *http.Request) {
		if v.discloseDirectory {
			writeFixtureJSON(w, map[string]any{"users": []string{fixturePrimaryProfile, fixtureDirectProfile, fixturePinProfile}})
			return
		}
		refuse(w)
	})
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			refuse(w)
			return
		}
		writeFixtureJSON(w, map[string]any{"id": fixturePrimaryProfile, "type": "user"})
	})
	mux.HandleFunc("GET /api/me/listening-sessions", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			refuse(w)
			return
		}
		sessions := []map[string]any{}
		if v.leakAdultActivity {
			sessions = append(sessions, map[string]any{"libraryItemId": fixtureAdultItem})
		}
		writeFixtureJSON(w, map[string]any{"sessions": sessions})
	})
	mux.HandleFunc("GET /api/libraries/{id}/items", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			refuse(w)
			return
		}
		results := []map[string]any{{"id": fixtureOrdinaryItem}}
		if v.discloseAdultCatalog {
			results = append(results, map[string]any{"id": fixtureAdultItem})
		}
		writeFixtureJSON(w, map[string]any{"results": results, "total": 1, "page": 0})
	})
	mux.HandleFunc("GET /api/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		if v.discloseForeignItem && r.PathValue("id") == fixtureForeignItem {
			writeFixtureJSON(w, map[string]any{"id": fixtureForeignItem})
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /api/items/{id}/cover", func(w http.ResponseWriter, r *http.Request) {
		if v.discloseAdultArtwork && r.PathValue("id") == fixtureAdultItem {
			_, _ = w.Write([]byte("adult-image-001"))
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("POST /api/items/{id}/play", func(w http.ResponseWriter, r *http.Request) {
		if v.discloseAdultPlayback && r.PathValue("id") == fixtureAdultItem {
			writeFixtureJSON(w, map[string]any{"id": "fixture-play-session-401", "libraryItemId": fixtureAdultItem})
			return
		}
		http.NotFound(w, r)
	})
	return httptest.NewServer(mux)
}

func TestAudiobookshelfIdentityContractFreezesSubjectSemantics(t *testing.T) {
	compliant := newAudiobookshelfIdentityListener(t, audiobookshelfViolations{})
	defer compliant.Close()
	report, err := Run(context.Background(), Target{BaseURL: compliant.URL, Client: compliant.Client()}, AudiobookshelfIdentity())
	if err != nil {
		t.Fatalf("compliant listener: %v; report=%s", err, report.JSON())
	}

	for _, tt := range []struct {
		name       string
		violations audiobookshelfViolations
		failing    string
	}{
		{"disclosed user directory", audiobookshelfViolations{discloseDirectory: true}, "unknown caller receives no profile directory"},
		{"direct login disclosing siblings", audiobookshelfViolations{discloseSiblings: true}, "direct profile login binds exactly one profile"},
		{"direct login granted root", audiobookshelfViolations{grantRoot: true}, "direct profile login binds exactly one profile"},
		{"stale revisions accepted", audiobookshelfViolations{acceptStaleTokens: true}, "a stale security revision is refused"},
		{"cross organization disclosure", audiobookshelfViolations{discloseForeignItem: true}, "cross organization item is not disclosed"},
		{"adult item in catalog count", audiobookshelfViolations{discloseAdultCatalog: true}, "ordinary catalog count excludes adult items"},
		{"adult artwork served", audiobookshelfViolations{discloseAdultArtwork: true}, "adult artwork is not disclosed"},
		{"adult playback started", audiobookshelfViolations{discloseAdultPlayback: true}, "adult playback is not disclosed"},
		{"adult activity leaked", audiobookshelfViolations{leakAdultActivity: true}, "activity omits adult items"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newAudiobookshelfIdentityListener(t, tt.violations)
			defer server.Close()
			_, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, AudiobookshelfIdentity())
			if err == nil {
				t.Fatal("a violating listener passed the identity contract")
			}
			if !strings.Contains(err.Error(), tt.failing) {
				t.Fatalf("failure = %v, want it to name %q", err, tt.failing)
			}
		})
	}
}

func allFixtureSuites() []Suite {
	return []Suite{
		JellyfinBaseline(),
		AudiobookshelfBaseline(),
		JellyfinOrdinaryAdultPolicy(),
		JellyfinAuthorizedAdultPolicy(),
		AudiobookshelfOrdinaryAdultPolicy(),
		AudiobookshelfAuthorizedAdultPolicy(),
		JellyfinIdentity(),
		AudiobookshelfIdentity(),
	}
}

var (
	fixtureURLPattern   = regexp.MustCompile(`https?://([^/"'\s]+)`)
	fixtureEmailPattern = regexp.MustCompile(`[A-Za-z0-9._%+-]+@([A-Za-z0-9.-]+)`)
)

func reservedFixtureHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	for _, suffix := range []string{".example", ".test", ".invalid", ".localhost", ".example.com", ".example.net", ".example.org"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	switch host {
	case "example", "test", "invalid", "localhost", "example.com", "example.net", "example.org":
		return true
	}
	return false
}

// TestBaselineFixturesUseReservedValues pins that every embedded suite is
// named, complete, deterministic, and built only from invented identifiers:
// unique case names, relative paths, and reserved-domain hosts and email
// domains throughout.
func TestBaselineFixturesUseReservedValues(t *testing.T) {
	for _, suite := range allFixtureSuites() {
		if suite.Name == "" || len(suite.Cases) == 0 {
			t.Fatalf("suite = %#v, want a named suite with cases", suite)
		}
		seen := map[string]bool{}
		for _, c := range suite.Cases {
			if c.Name == "" || c.Path == "" {
				t.Fatalf("%s has incomplete case %#v", suite.Name, c)
			}
			if seen[c.Name] {
				t.Fatalf("%s repeats case name %q", suite.Name, c.Name)
			}
			seen[c.Name] = true

			caseURL, err := url.Parse(c.Path)
			if err != nil || caseURL.IsAbs() || caseURL.Host != "" {
				t.Fatalf("%s case %q path %q is not relative", suite.Name, c.Name, c.Path)
			}

			var text []string
			text = append(text, c.Path, string(c.Body))
			text = append(text, c.PresentStrings...)
			text = append(text, c.AbsentStrings...)
			for _, value := range c.Headers {
				text = append(text, value)
			}
			for _, want := range c.WantHeaders {
				text = append(text, want)
			}
			text = append(text, string(c.WantJSON))
			for _, blob := range text {
				for _, match := range fixtureURLPattern.FindAllStringSubmatch(blob, -1) {
					if !reservedFixtureHost(match[1]) {
						t.Fatalf("%s case %q uses non-reserved host %q", suite.Name, c.Name, match[1])
					}
				}
				for _, match := range fixtureEmailPattern.FindAllStringSubmatch(blob, -1) {
					if !reservedFixtureHost(match[1]) {
						t.Fatalf("%s case %q uses non-reserved email domain %q", suite.Name, c.Name, match[1])
					}
				}
			}
		}
	}
}

// TestIdentitySuitesPinTimingDistributions freezes the documented timing
// tolerance: the missing-adult and random-missing distributions are compared
// over at least 100 samples with at most a 3x mean ratio (the runner adds a
// fixed 20ms absolute allowance for scheduler noise).
func TestIdentitySuitesPinTimingDistributions(t *testing.T) {
	for _, suite := range []Suite{JellyfinIdentity(), AudiobookshelfIdentity()} {
		found := false
		for _, c := range suite.Cases {
			if c.Timing == nil {
				continue
			}
			found = true
			if c.Timing.ControlPath == "" {
				t.Errorf("%s case %q has no timing control path", suite.Name, c.Name)
			}
			if c.Timing.Samples < 100 {
				t.Errorf("%s case %q compares %d samples, want at least 100", suite.Name, c.Name, c.Timing.Samples)
			}
			if c.Timing.MaxRatio <= 1 || c.Timing.MaxRatio > 3 {
				t.Errorf("%s case %q allows ratio %v, want a tolerance in (1, 3]", suite.Name, c.Name, c.Timing.MaxRatio)
			}
		}
		if !found {
			t.Errorf("%s has no timing distribution case", suite.Name)
		}
	}
}

// TestIdentitySuitesNameEveryRevocationRevision freezes that each revocation
// revision keeps its own named case in both identity suites.
func TestIdentitySuitesNameEveryRevocationRevision(t *testing.T) {
	for _, suite := range []Suite{JellyfinIdentity(), AudiobookshelfIdentity()} {
		names := map[string]bool{}
		for _, c := range suite.Cases {
			names[c.Name] = true
		}
		for _, required := range identityRevocationCaseNames {
			if !names[required] {
				t.Errorf("%s is missing revocation case %q", suite.Name, required)
			}
		}
	}
}
