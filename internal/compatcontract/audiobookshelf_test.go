package compatcontract

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Audiobookshelf identity reference listener, mimicking the embedded ABS
// handler's real semantics: account login resolving the primary profile,
// username#profile selection with the account password, per-request token
// validation, and logout revocation. Observed over public HTTP only.
// ---------------------------------------------------------------------------

func audiobookshelfIdentityBindings() map[string]string {
	return map[string]string{
		"account_username":  refAccountUsername,
		"account_password":  refAccountPassword,
		"wrong_password":    refWrongPassword,
		"account_id":        refAccountID,
		"primary_profile":   refPrimaryProfile,
		"reader_profile":    refReaderProfile,
		"unknown_profile":   refUnknownProfile,
		"missing_adult_id":  refMissingAdultID,
		"missing_random_id": refMissingRandomID,
		"abs_account_token": "fixture-preseeded-abs-account-token",
		"abs_reader_token":  "fixture-preseeded-abs-reader-token",
	}
}

// audiobookshelfViolations seed exactly one contract breach each; the zero
// value is a compliant reference listener.
type audiobookshelfViolations struct {
	acceptAnonymousMe     bool
	acceptWrongPassword   bool
	misresolvePrimary     bool
	rootOnExplicitLogin   bool
	acceptUnknownProfile  bool
	misattributePrincipal bool
	acceptMalformedToken  bool
	logoutWrongStatus     bool
	ignoreLogout          bool
	delayAdultMiss        bool
	unequalControlStatus  bool
}

func newAudiobookshelfIdentityListener(t *testing.T, v audiobookshelfViolations) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	nextToken := 0
	tokens := map[string]string{ // token -> profile display name
		"fixture-preseeded-abs-account-token": refPrimaryProfile,
		"fixture-preseeded-abs-reader-token":  refReaderProfile,
	}
	bearer := func(r *http.Request) string {
		return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	principal := func(r *http.Request) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		profile, ok := tokens[bearer(r)]
		if !ok && v.acceptMalformedToken && bearer(r) == "fixture-not-a-real-token" {
			return refPrimaryProfile, true
		}
		return profile, ok
	}
	refuse := func(w http.ResponseWriter) { http.Error(w, "unauthorized", http.StatusUnauthorized) }

	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		var creds struct{ Username, Password string }
		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		account, profileSelector := creds.Username, ""
		if i := strings.LastIndexByte(creds.Username, '#'); i >= 0 {
			account, profileSelector = creds.Username[:i], creds.Username[i+1:]
		}
		if account != refAccountUsername || (creds.Password != refAccountPassword && !v.acceptWrongPassword) {
			refuse(w)
			return
		}
		profile := refPrimaryProfile
		if v.misresolvePrimary {
			profile = refReaderProfile
		}
		if profileSelector != "" {
			switch profileSelector {
			case refPrimaryProfile, refReaderProfile:
				profile = profileSelector
			default:
				if !v.acceptUnknownProfile {
					refuse(w)
					return
				}
				profile = profileSelector
			}
		}
		userType := "user"
		if v.rootOnExplicitLogin && profileSelector == refReaderProfile {
			userType = "root"
		}
		mu.Lock()
		nextToken++
		token := fmt.Sprintf("fixture-abs-session-%03d", nextToken)
		tokens[token] = profile
		mu.Unlock()
		writeFixtureJSON(w, map[string]any{
			"user": map[string]any{
				"id":          refAccountID,
				"username":    profile,
				"type":        userType,
				"accessToken": token,
			},
		})
	})
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		if v.acceptAnonymousMe && bearer(r) == "" {
			writeFixtureJSON(w, map[string]any{"id": refAccountID, "username": refPrimaryProfile, "profiles": []string{refPrimaryProfile, refReaderProfile}})
			return
		}
		profile, ok := principal(r)
		if !ok {
			refuse(w)
			return
		}
		if v.misattributePrincipal {
			profile = refPrimaryProfile
		}
		writeFixtureJSON(w, map[string]any{"id": refAccountID, "username": profile, "type": "user"})
	})
	mux.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if !v.ignoreLogout {
			delete(tokens, bearer(r))
		}
		mu.Unlock()
		if v.logoutWrongStatus {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeFixtureJSON(w, map[string]any{"redirect_url": nil})
	})
	mux.HandleFunc("GET /api/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := principal(r); !ok {
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

func runAudiobookshelfReference(t *testing.T, v audiobookshelfViolations, suite Suite) (Report, error) {
	t.Helper()
	server := newAudiobookshelfIdentityListener(t, v)
	defer server.Close()
	return Run(context.Background(), Target{
		BaseURL:  server.URL,
		Client:   server.Client(),
		Bindings: audiobookshelfIdentityBindings(),
	}, suite)
}

func TestAudiobookshelfIdentityContractFreezesSubjectSemantics(t *testing.T) {
	report, err := runAudiobookshelfReference(t, audiobookshelfViolations{}, AudiobookshelfIdentity())
	if err != nil {
		t.Fatalf("compliant listener: %v; report=%s", err, report.JSON())
	}

	for _, tt := range []struct {
		name       string
		violations audiobookshelfViolations
		failing    string
	}{
		{"anonymous principal answered", audiobookshelfViolations{acceptAnonymousMe: true}, "an unknown caller is refused"},
		{"wrong password accepted", audiobookshelfViolations{acceptWrongPassword: true}, "a wrong password is refused"},
		{"wrong remembered profile", audiobookshelfViolations{misresolvePrimary: true}, "legacy account login resolves the remembered profile"},
		{"root granted on login", audiobookshelfViolations{rootOnExplicitLogin: true}, "an explicit profile selection uses the account password"},
		{"unknown profile accepted", audiobookshelfViolations{acceptUnknownProfile: true}, "an unknown profile is indistinguishable from bad credentials"},
		{"principal misattributed", audiobookshelfViolations{misattributePrincipal: true}, "the token answers for its own principal"},
		{"malformed token accepted", audiobookshelfViolations{acceptMalformedToken: true}, "a malformed token is refused"},
		{"logout answers the wrong status", audiobookshelfViolations{logoutWrongStatus: true}, "logging out revokes the token"},
		{"logout ignored", audiobookshelfViolations{ignoreLogout: true}, "a revoked token is refused"},
		{"stable adult delay", audiobookshelfViolations{delayAdultMiss: true}, "missing adult and missing random ids time alike"},
		{"control status differs", audiobookshelfViolations{unequalControlStatus: true}, "missing adult and missing random ids time alike"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report, err := runAudiobookshelfReference(t, tt.violations, AudiobookshelfIdentity())
			requireSingleFailure(t, report, err, tt.failing)
		})
	}
}

// ---------------------------------------------------------------------------
// Fixture hygiene: every embedded suite stays deterministic and reserved.
// ---------------------------------------------------------------------------

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
		DirectProfileIdentity(),
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

			caseURL, err := url.Parse(strings.NewReplacer("{{", "", "}}", "").Replace(c.Path))
			if err != nil || caseURL.IsAbs() || caseURL.Host != "" {
				t.Fatalf("%s case %q path %q is not relative", suite.Name, c.Name, c.Path)
			}

			var text []string
			text = append(text, c.Path, string(c.Body), string(c.BodyJSON))
			text = append(text, c.PresentStrings...)
			text = append(text, c.AbsentStrings...)
			for _, value := range c.Headers {
				text = append(text, value)
			}
			for _, want := range c.WantHeaders {
				text = append(text, want)
			}
			for _, want := range c.WantJSONFields {
				text = append(text, string(want))
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
// tolerance: the missing-adult and random-missing medians are compared over
// at least 100 interleaved samples with at most a 3x ratio (the runner adds a
// fixed 2ms absolute floor for scheduler noise, and every sample must answer
// the same status).
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

// TestIdentitySuitesNameTheirRevocationCases freezes that each surface keeps
// its own named revocation coverage: a case removed from a suite fails here
// before any listener is consulted.
func TestIdentitySuitesNameTheirRevocationCases(t *testing.T) {
	required := map[string][]string{
		JellyfinIdentity().Name: {
			"logging out revokes the compat session",
			"a logged out session is refused",
			"a revoked account session is refused",
		},
		AudiobookshelfIdentity().Name: {
			"logging out revokes the token",
			"a revoked token is refused",
			"a malformed token is refused",
		},
		DirectProfileIdentity().Name: {
			"a stale organization policy revision is refused",
			"a stale membership security revision is refused",
			"a reset profile credential is refused",
		},
	}
	for _, suite := range []Suite{JellyfinIdentity(), AudiobookshelfIdentity(), DirectProfileIdentity()} {
		names := map[string]bool{}
		for _, c := range suite.Cases {
			names[c.Name] = true
		}
		for _, want := range required[suite.Name] {
			if !names[want] {
				t.Errorf("%s is missing revocation case %q", suite.Name, want)
			}
		}
	}
}

// TestIdentitySuitesChainTheIssuedCredential freezes the chain the review
// demanded: each suite's login case captures the credential the login
// actually issues, and the cases that bound that credential's privileges
// spend the same binding rather than an unrelated fixture token.
func TestIdentitySuitesChainTheIssuedCredential(t *testing.T) {
	requireChain := func(t *testing.T, suite Suite, loginCase, binding string, spenders []string) {
		t.Helper()
		byName := map[string]Case{}
		for _, c := range suite.Cases {
			byName[c.Name] = c
		}
		login, ok := byName[loginCase]
		if !ok {
			t.Fatalf("%s is missing login case %q", suite.Name, loginCase)
		}
		captured := false
		for name := range login.Capture {
			if name == binding {
				captured = true
			}
		}
		if !captured {
			t.Fatalf("%s login case %q does not capture %q", suite.Name, loginCase, binding)
		}
		placeholder := "{{" + binding + "}}"
		for _, spender := range spenders {
			c, ok := byName[spender]
			if !ok {
				t.Errorf("%s is missing chained case %q", suite.Name, spender)
				continue
			}
			spends := strings.Contains(c.Path, placeholder)
			for _, value := range c.Headers {
				if strings.Contains(value, placeholder) {
					spends = true
				}
			}
			if !spends {
				t.Errorf("%s case %q does not spend the captured %s", suite.Name, spender, binding)
			}
		}
	}

	requireChain(t, DirectProfileIdentity(), "direct profile login binds exactly one profile", "direct_token", []string{
		"the profile directory is refused",
		"household session management is refused",
		"a sibling profile update is refused",
		"minting an account api key is refused",
		"verifying a profile pin is refused",
		"the bound profile reads its own record",
		"the bound profile updates its own record",
	})
	requireChain(t, JellyfinIdentity(), "an explicit profile selection needs no pin", "jf_reader_token", []string{
		"the session sees itself and nothing more",
		"a sibling user id is not disclosed",
		"another household's user id is not disclosed",
		"the current user endpoint answers the bound session",
		"events omit adult items",
		"missing adult and missing random ids time alike",
	})
	requireChain(t, AudiobookshelfIdentity(), "an explicit profile selection uses the account password", "abs_reader_token", []string{
		"the token answers for its own principal",
		"logging out revokes the token",
		"a revoked token is refused",
	})
}
