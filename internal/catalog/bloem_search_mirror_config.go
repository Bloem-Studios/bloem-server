package catalog

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
)

// Environment variables that switch on and set up the search mirror, a second
// Meilisearch that holds a copy of the catalog index. Unlike the main search
// variables, the mirror's values are read once at start, are never written to
// server_settings and are never shown in full: they live only in the process.
// A blank or unset URL leaves the mirror off. Every Silo process sharing one
// database must name the same mirror.
const (
	EnvSearchMirrorURL        = "SILO_MEILISEARCH_MIRROR_URL"
	EnvSearchMirrorAPIKeyFile = "SILO_MEILISEARCH_MIRROR_API_KEY_FILE"
	EnvSearchMirrorAPIKey     = "SILO_MEILISEARCH_MIRROR_API_KEY"
	EnvSearchMirrorMaxLag     = "SILO_MEILISEARCH_MIRROR_MAX_LAG"

	// DefaultSearchMirrorMaxLag is how far behind the mirror may fall before it
	// stops catching up and makes a fresh full copy.
	DefaultSearchMirrorMaxLag = 72 * time.Hour
	// SearchMirrorSwitchingMaxLag is the most the lag may be while
	// bloem_silo_switching is enabled. The ceiling is applied at run time, not
	// when the settings are read.
	SearchMirrorSwitchingMaxLag = 20 * time.Hour
)

// SearchMirrorProblem names why a configured mirror is not running. The empty
// value means there is none.
type SearchMirrorProblem string

const (
	SearchMirrorProblemURL      SearchMirrorProblem = "invalid_url"
	SearchMirrorProblemBothKeys SearchMirrorProblem = "both_keys_set"
	SearchMirrorProblemKeyFile  SearchMirrorProblem = "key_file_unreadable"
	SearchMirrorProblemSameURL  SearchMirrorProblem = "same_address_as_main"
	SearchMirrorProblemMaxLag   SearchMirrorProblem = "invalid_max_lag"
	// SearchMirrorProblemKeyRejected is set at run time, when the mirror server
	// answers 401 or 403.
	SearchMirrorProblemKeyRejected SearchMirrorProblem = "key_rejected"
	// SearchMirrorProblemSameServer is set at run time, when the identity
	// markers show the mirror address reaches the main server.
	SearchMirrorProblemSameServer SearchMirrorProblem = "same_server"
)

const (
	searchMirrorKeySourceFile        = "file"
	searchMirrorKeySourceEnvironment = "environment"
	searchMirrorKeySourceNone        = "none"
)

// SearchMirrorConfig is the mirror's settings as the environment gave them.
type SearchMirrorConfig struct {
	Configured bool   // the URL variable is non-blank
	URL        string // normalized the way the main address is; empty when it was invalid
	APIKey     string // never logged or returned
	KeySource  string // "file", "environment" or "none"
	MaxLag     time.Duration
	Problem    SearchMirrorProblem
}

// LoadSearchMirrorConfig reads the mirror's settings. getenv and readFile are
// the process environment and file reader, passed in so tests can stand in for
// them; mainURL is the main index's address, used to refuse a mirror that
// points at the main server.
//
// It never fails: a fault in the settings is the Problem, and only the first
// one found is reported. A configuration with a Problem carries no key. The
// mirror never borrows the main index's key: sending it to a mistyped address
// would leak it, and a Meilisearch with no master key needs none.
func LoadSearchMirrorConfig(getenv func(string) string, readFile func(string) ([]byte, error), mainURL string) SearchMirrorConfig {
	cfg := SearchMirrorConfig{KeySource: searchMirrorKeySourceNone, MaxLag: DefaultSearchMirrorMaxLag}

	rawURL := strings.TrimSpace(getenv(EnvSearchMirrorURL))
	if rawURL == "" {
		return cfg
	}
	cfg.Configured = true

	mirrorURL, err := config.NormalizeAdminSetting(SearchSettingMeilisearchURL, rawURL)
	if err != nil {
		cfg.Problem = SearchMirrorProblemURL
		return cfg
	}
	cfg.URL = mirrorURL

	key, keySource, problem := loadSearchMirrorKey(getenv, readFile)
	if problem != "" {
		cfg.Problem = problem
		return cfg
	}

	// A main address that is unset or unusable cannot be the mirror's.
	if main, err := config.NormalizeAdminSetting(SearchSettingMeilisearchURL, mainURL); err == nil && main == mirrorURL {
		cfg.Problem = SearchMirrorProblemSameURL
		return cfg
	}

	if rawLag := strings.TrimSpace(getenv(EnvSearchMirrorMaxLag)); rawLag != "" {
		lag, err := time.ParseDuration(rawLag)
		if err != nil || lag <= 0 {
			cfg.Problem = SearchMirrorProblemMaxLag
			return cfg
		}
		cfg.MaxLag = lag
	}

	cfg.APIKey, cfg.KeySource = key, keySource
	return cfg
}

// loadSearchMirrorKey resolves the mirror's key from its file or its variable.
// A file with nothing in it, like no variable at all, means no key.
func loadSearchMirrorKey(getenv func(string) string, readFile func(string) ([]byte, error)) (key, source string, problem SearchMirrorProblem) {
	keyFile := strings.TrimSpace(getenv(EnvSearchMirrorAPIKeyFile))
	keyValue := strings.TrimSpace(getenv(EnvSearchMirrorAPIKey))
	switch {
	case keyFile != "" && keyValue != "":
		return "", searchMirrorKeySourceNone, SearchMirrorProblemBothKeys
	case keyFile != "":
		content, err := readFile(keyFile)
		if err != nil {
			return "", searchMirrorKeySourceNone, SearchMirrorProblemKeyFile
		}
		if key = strings.TrimSpace(string(content)); key != "" {
			return key, searchMirrorKeySourceFile, ""
		}
	case keyValue != "":
		return keyValue, searchMirrorKeySourceEnvironment, ""
	}
	return "", searchMirrorKeySourceNone, ""
}

// Active reports whether the mirror is switched on and has nothing wrong with it.
func (c SearchMirrorConfig) Active() bool {
	return c.Configured && c.Problem == ""
}

// RedactedURL is the mirror's address with any user name and password removed,
// safe to log and to show. An address that cannot be parsed comes back empty.
func (c SearchMirrorConfig) RedactedURL() string {
	parsed, err := url.Parse(c.URL)
	if err != nil {
		return ""
	}
	parsed.User = nil
	return parsed.String()
}

// LogMessage is the one English line logged when the mirror is switched off by
// a problem. It names the variable at fault and never contains a key or the
// address's credentials. It is empty when there is no problem.
func (c SearchMirrorConfig) LogMessage() string {
	var detail string
	switch c.Problem {
	case "":
		return ""
	case SearchMirrorProblemURL:
		detail = EnvSearchMirrorURL + " is not an http or https address with a host"
	case SearchMirrorProblemBothKeys:
		detail = "set only one of " + EnvSearchMirrorAPIKeyFile + " and " + EnvSearchMirrorAPIKey
	case SearchMirrorProblemKeyFile:
		detail = "the file named by " + EnvSearchMirrorAPIKeyFile + " could not be read"
	case SearchMirrorProblemSameURL:
		detail = fmt.Sprintf("%s (%s) is the same address as the main search server", EnvSearchMirrorURL, c.RedactedURL())
	case SearchMirrorProblemMaxLag:
		detail = EnvSearchMirrorMaxLag + " must be a duration above zero, such as 72h"
	case SearchMirrorProblemKeyRejected:
		detail = fmt.Sprintf("the server at %s rejected the mirror API key", c.RedactedURL())
	case SearchMirrorProblemSameServer:
		detail = fmt.Sprintf("the address %s reaches the same Meilisearch server as the main index", c.RedactedURL())
	default:
		detail = "the mirror settings are not usable"
	}
	return fmt.Sprintf("search mirror misconfigured (%s): %s; the mirror stays off", c.Problem, detail)
}
