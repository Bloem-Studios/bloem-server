package catalog

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const searchMirrorTestMainURL = "http://silo-search:7700"

// envOf serves a fixed environment; a name that is absent reads as unset.
func envOf(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// filesOf serves fixed file contents; any other path does not exist.
func filesOf(files map[string]string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		content, ok := files[path]
		if !ok {
			return nil, os.ErrNotExist
		}
		return []byte(content), nil
	}
}

// noFiles is a file system with no files in it.
func noFiles(string) ([]byte, error) { return nil, os.ErrNotExist }

// describeMirrorConfig renders a config for a failure message with the key
// masked, so a failing test never prints a key value.
func describeMirrorConfig(c SearchMirrorConfig) string {
	if c.APIKey != "" {
		c.APIKey = "<set>"
	}
	return fmt.Sprintf("%+v", c)
}

func TestSearchMirrorConfigOffWhenURLUnsetOrBlank(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"unset": {},
		"empty": {EnvSearchMirrorURL: ""},
		"blank": {EnvSearchMirrorURL: "   "},
		// Without the address the other variables are not looked at, however wrong.
		"only the other variables": {
			EnvSearchMirrorURL:        " ",
			EnvSearchMirrorAPIKey:     "a-key",
			EnvSearchMirrorAPIKeyFile: "/missing",
			EnvSearchMirrorMaxLag:     "soon",
		},
	} {
		c := LoadSearchMirrorConfig(envOf(env), noFiles, searchMirrorTestMainURL)
		if c.Configured || c.Active() || c.Problem != "" || c.APIKey != "" || c.URL != "" {
			t.Fatalf("%s: %s", name, describeMirrorConfig(c))
		}
		if c.LogMessage() != "" {
			t.Fatalf("%s: an off mirror must log nothing, got %q", name, c.LogMessage())
		}
	}
}

func TestSearchMirrorConfigReadsKeyFileTrimmed(t *testing.T) {
	c := LoadSearchMirrorConfig(envOf(map[string]string{EnvSearchMirrorURL: "http://silo-search-mirror:7700/", EnvSearchMirrorAPIKeyFile: "/k"}),
		filesOf(map[string]string{"/k": "s3cret\n"}), searchMirrorTestMainURL)
	if !c.Active() || c.APIKey != "s3cret" || c.KeySource != "file" || c.URL != "http://silo-search-mirror:7700" || c.MaxLag != 72*time.Hour {
		t.Fatal(describeMirrorConfig(c))
	}
	if !c.Configured || c.Problem != "" {
		t.Fatal(describeMirrorConfig(c))
	}
}

func TestSearchMirrorConfigTrimsEveryKindOfSurroundingSpace(t *testing.T) {
	c := LoadSearchMirrorConfig(envOf(map[string]string{EnvSearchMirrorURL: "  http://m:7700/  ", EnvSearchMirrorAPIKeyFile: " /k "}),
		filesOf(map[string]string{"/k": " \t s3cret \r\n\n"}), searchMirrorTestMainURL)
	if !c.Active() || c.APIKey != "s3cret" || c.URL != "http://m:7700" {
		t.Fatal(describeMirrorConfig(c))
	}
}

func TestSearchMirrorConfigEmptyKeyFileMeansNoKey(t *testing.T) {
	for _, content := range []string{"\n", "", "  \r\n"} {
		c := LoadSearchMirrorConfig(envOf(map[string]string{EnvSearchMirrorURL: "http://m:7700", EnvSearchMirrorAPIKeyFile: "/k"}),
			filesOf(map[string]string{"/k": content}), searchMirrorTestMainURL)
		if !c.Active() || c.APIKey != "" || c.KeySource != "none" {
			t.Fatalf("%q: %s", content, describeMirrorConfig(c))
		}
	}
}

func TestSearchMirrorConfigKeyVariable(t *testing.T) {
	c := LoadSearchMirrorConfig(envOf(map[string]string{EnvSearchMirrorURL: "http://m:7700", EnvSearchMirrorAPIKey: " envkey \n"}),
		noFiles, searchMirrorTestMainURL)
	if !c.Active() || c.APIKey != "envkey" || c.KeySource != "environment" {
		t.Fatal(describeMirrorConfig(c))
	}
}

func TestSearchMirrorConfigNoKeyVariablesMeansNoKey(t *testing.T) {
	c := LoadSearchMirrorConfig(envOf(map[string]string{EnvSearchMirrorURL: "http://m:7700"}), noFiles, searchMirrorTestMainURL)
	if !c.Active() || c.APIKey != "" || c.KeySource != "none" {
		t.Fatal(describeMirrorConfig(c))
	}
}

func TestSearchMirrorConfigProblems(t *testing.T) {
	const (
		envSecret  = "envsecret-4f1a"
		fileSecret = "filesecret-91bc"
		urlSecret  = "urlsecret-77de"
	)
	files := filesOf(map[string]string{"/k": fileSecret + "\n"})
	cases := map[SearchMirrorProblem]map[string]string{
		SearchMirrorProblemURL:      {EnvSearchMirrorURL: "silo-search-mirror:7700"},
		SearchMirrorProblemBothKeys: {EnvSearchMirrorURL: "http://m:7700", EnvSearchMirrorAPIKey: envSecret, EnvSearchMirrorAPIKeyFile: "/k"},
		SearchMirrorProblemKeyFile:  {EnvSearchMirrorURL: "http://m:7700", EnvSearchMirrorAPIKeyFile: "/missing"},
		SearchMirrorProblemSameURL:  {EnvSearchMirrorURL: "http://silo-search:7700/"},
		SearchMirrorProblemMaxLag:   {EnvSearchMirrorURL: "http://m:7700", EnvSearchMirrorMaxLag: "soon"},
	}
	// Variants that put a secret next to each fault: none may reach the message.
	withSecrets := map[string]struct {
		problem SearchMirrorProblem
		env     map[string]string
		main    string
	}{
		"address that does not parse": {SearchMirrorProblemURL, map[string]string{EnvSearchMirrorURL: "user:" + urlSecret + "@m:7700"}, searchMirrorTestMainURL},
		"address that is the main one": {SearchMirrorProblemSameURL, map[string]string{
			EnvSearchMirrorURL:    "http://user:" + urlSecret + "@silo-search:7700",
			EnvSearchMirrorAPIKey: envSecret,
		}, "http://user:" + urlSecret + "@silo-search:7700"},
		"lag after a key file": {SearchMirrorProblemMaxLag, map[string]string{
			EnvSearchMirrorURL:        "http://user:" + urlSecret + "@m:7700",
			EnvSearchMirrorAPIKeyFile: "/k",
			EnvSearchMirrorMaxLag:     "0s",
		}, searchMirrorTestMainURL},
	}

	check := func(name string, want SearchMirrorProblem, c SearchMirrorConfig) {
		t.Helper()
		if !c.Configured || c.Active() || c.Problem != want || c.APIKey != "" || c.KeySource != "none" {
			t.Fatalf("%s: %s", name, describeMirrorConfig(c))
		}
		msg := c.LogMessage()
		if msg == "" || strings.ContainsAny(msg, "\r\n") {
			t.Fatalf("%s: the message must be one non-empty line, got %q", name, msg)
		}
		if !strings.Contains(msg, string(want)) {
			t.Fatalf("%s: the message should carry the problem code %q, got %q", name, want, msg)
		}
		for _, secret := range []string{envSecret, fileSecret, urlSecret} {
			if strings.Contains(msg, secret) {
				t.Fatalf("%s: the message leaks a secret: %q", name, msg)
			}
		}
	}

	for want, env := range cases {
		check(string(want), want, LoadSearchMirrorConfig(envOf(env), files, searchMirrorTestMainURL))
	}
	for name, tc := range withSecrets {
		check(name, tc.problem, LoadSearchMirrorConfig(envOf(tc.env), files, tc.main))
	}
}

func TestSearchMirrorConfigOnlyTheFirstProblemIsReported(t *testing.T) {
	// Bad address, both keys, unreadable file, main address, bad lag: the
	// address is checked first, then the keys, the main address, the lag.
	env := map[string]string{
		EnvSearchMirrorURL:        "not a url",
		EnvSearchMirrorAPIKey:     "k",
		EnvSearchMirrorAPIKeyFile: "/missing",
		EnvSearchMirrorMaxLag:     "soon",
	}
	if c := LoadSearchMirrorConfig(envOf(env), noFiles, searchMirrorTestMainURL); c.Problem != SearchMirrorProblemURL {
		t.Fatalf("got %q", c.Problem)
	}
	env[EnvSearchMirrorURL] = searchMirrorTestMainURL
	if c := LoadSearchMirrorConfig(envOf(env), noFiles, searchMirrorTestMainURL); c.Problem != SearchMirrorProblemBothKeys {
		t.Fatalf("got %q", c.Problem)
	}
	delete(env, EnvSearchMirrorAPIKey)
	if c := LoadSearchMirrorConfig(envOf(env), noFiles, searchMirrorTestMainURL); c.Problem != SearchMirrorProblemKeyFile {
		t.Fatalf("got %q", c.Problem)
	}
	delete(env, EnvSearchMirrorAPIKeyFile)
	if c := LoadSearchMirrorConfig(envOf(env), noFiles, searchMirrorTestMainURL); c.Problem != SearchMirrorProblemSameURL {
		t.Fatalf("got %q", c.Problem)
	}
	env[EnvSearchMirrorURL] = "http://m:7700"
	if c := LoadSearchMirrorConfig(envOf(env), noFiles, searchMirrorTestMainURL); c.Problem != SearchMirrorProblemMaxLag {
		t.Fatalf("got %q", c.Problem)
	}
}

func TestSearchMirrorConfigSameAddressComparesNormalizedURLs(t *testing.T) {
	env := envOf(map[string]string{EnvSearchMirrorURL: "http://silo-search:7700"})
	for _, main := range []string{"http://silo-search:7700", "http://silo-search:7700/", "  http://silo-search:7700//  "} {
		if c := LoadSearchMirrorConfig(env, noFiles, main); c.Problem != SearchMirrorProblemSameURL {
			t.Fatalf("main %q: %s", main, describeMirrorConfig(c))
		}
	}
	// A main address that is unset, unusable or elsewhere cannot be the same.
	for _, main := range []string{"", "   ", "silo-search:7700", "http://silo-search:7701", "https://silo-search:7700", "http://other:7700"} {
		if c := LoadSearchMirrorConfig(env, noFiles, main); !c.Active() {
			t.Fatalf("main %q: %s", main, describeMirrorConfig(c))
		}
	}
}

func TestSearchMirrorConfigMaxLagParsed(t *testing.T) {
	load := func(lag string) SearchMirrorConfig {
		return LoadSearchMirrorConfig(envOf(map[string]string{EnvSearchMirrorURL: "http://m:7700", EnvSearchMirrorMaxLag: lag}), noFiles, searchMirrorTestMainURL)
	}
	for lag, want := range map[string]time.Duration{
		"36h":      36 * time.Hour,
		" 90m ":    90 * time.Minute,
		"1h30m":    90 * time.Minute,
		"100h":     100 * time.Hour, // the 20 h switching ceiling is applied at run time, not here
		"1s":       time.Second,
		"":         DefaultSearchMirrorMaxLag,
		"   ":      DefaultSearchMirrorMaxLag,
		"72h":      DefaultSearchMirrorMaxLag,
		"0.5h":     30 * time.Minute,
		"1000000h": 1000000 * time.Hour,
	} {
		c := load(lag)
		if !c.Active() || c.MaxLag != want {
			t.Fatalf("%q: want %s, got %s", lag, want, describeMirrorConfig(c))
		}
	}
	for _, lag := range []string{"0s", "0", "-1h", "soon", "36", "3d", "1h-"} {
		c := load(lag)
		if c.Active() || c.Problem != SearchMirrorProblemMaxLag || c.MaxLag != DefaultSearchMirrorMaxLag {
			t.Fatalf("%q: %s", lag, describeMirrorConfig(c))
		}
	}
}

func TestSearchMirrorConfigRedactsUserinfo(t *testing.T) {
	c := LoadSearchMirrorConfig(envOf(map[string]string{EnvSearchMirrorURL: "http://u:p@m:7700"}), noFiles, searchMirrorTestMainURL)
	if !c.Active() {
		t.Fatal(describeMirrorConfig(c))
	}
	if got := c.RedactedURL(); got != "http://m:7700" {
		t.Fatalf("RedactedURL() = %q", got)
	}
	if got := (SearchMirrorConfig{URL: "https://m.example/base"}).RedactedURL(); got != "https://m.example/base" {
		t.Fatalf("a URL with no userinfo must come back unchanged, got %q", got)
	}
	if got := (SearchMirrorConfig{}).RedactedURL(); got != "" {
		t.Fatalf("no URL must redact to nothing, got %q", got)
	}
	if got := (SearchMirrorConfig{URL: "%zz://u:p@m"}).RedactedURL(); strings.Contains(got, "u:p") {
		t.Fatalf("an unparsable URL must not come back with its userinfo, got %q", got)
	}
}

func TestSearchMirrorConfigNeverFallsBackToMainKey(t *testing.T) {
	// The main server's key is in the environment and the mirror has none: the
	// mirror must not borrow it, or a mistyped address would receive it.
	c := LoadSearchMirrorConfig(envOf(map[string]string{
		EnvSearchMirrorURL:   "http://m:7700",
		EnvMeilisearchAPIKey: "main-key-must-stay-home",
	}), noFiles, searchMirrorTestMainURL)
	if !c.Active() || c.APIKey != "" || c.KeySource != "none" {
		t.Fatal(describeMirrorConfig(c))
	}
}

func TestSearchMirrorConfigRuntimeProblemsHaveMessages(t *testing.T) {
	// These two are set while running, on a config that loaded cleanly.
	loaded := LoadSearchMirrorConfig(envOf(map[string]string{
		EnvSearchMirrorURL:    "http://user:urlsecret-77de@m:7700",
		EnvSearchMirrorAPIKey: "envsecret-4f1a",
	}), noFiles, searchMirrorTestMainURL)
	for _, problem := range []SearchMirrorProblem{SearchMirrorProblemKeyRejected, SearchMirrorProblemSameServer} {
		c := loaded
		c.Problem = problem
		msg := c.LogMessage()
		if c.Active() || msg == "" || strings.ContainsAny(msg, "\r\n") || !strings.Contains(msg, string(problem)) {
			t.Fatalf("%s: %q", problem, msg)
		}
		if strings.Contains(msg, "urlsecret-77de") || strings.Contains(msg, "envsecret-4f1a") {
			t.Fatalf("%s: the message leaks a secret: %q", problem, msg)
		}
	}
}

func TestSearchMirrorConfigActiveNeedsConfiguredAndNoProblem(t *testing.T) {
	for _, tc := range []struct {
		c    SearchMirrorConfig
		want bool
	}{
		{SearchMirrorConfig{}, false},
		{SearchMirrorConfig{Configured: true}, true},
		{SearchMirrorConfig{Configured: true, Problem: SearchMirrorProblemKeyRejected}, false},
		{SearchMirrorConfig{Problem: SearchMirrorProblemURL}, false},
	} {
		if got := tc.c.Active(); got != tc.want {
			t.Fatalf("%s: Active() = %v", describeMirrorConfig(tc.c), got)
		}
	}
}
