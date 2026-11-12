package nativestorage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConnectionSummaryS3ExposesOnlyNonSecretFields(t *testing.T) {
	conn := map[string]any{"source_id": "s3_1", "source_name": "Movies", "bucket": "media", "prefix": "movies/", "region": "us-east-1",
		"endpoint": "https://s3.example.com", "revision_mode": "checked-etag", "probe_key": "movies/x.mkv",
		"access_key": "AKIASECRETVALUE", "secret_key": "SECRETKEYVALUE", "session_token": "TOKENVALUE", "state_path": "/var/x"}
	got := ConnectionSummary("bloem.storage.s3", conn)
	want := map[string]string{"source_name": "Movies", "bucket": "media", "prefix": "movies/", "region": "us-east-1",
		"endpoint": "https://s3.example.com", "revision_mode": "checked-etag", "probe_key": "movies/x.mkv"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %q want %q", k, got[k], v)
		}
	}
	raw, _ := json.Marshal(got)
	for _, secret := range []string{"AKIASECRETVALUE", "SECRETKEYVALUE", "TOKENVALUE", "/var/x"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("summary leaked %q", secret)
		}
	}
}

func TestConnectionSummaryEmptyPrefixIsPresentAndUnknownPluginIsEmpty(t *testing.T) {
	if got := ConnectionSummary("bloem.storage.s3", map[string]any{"bucket": "b"}); got["prefix"] != "" || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if got := ConnectionSummary("bloem.storage.s3", nil); got == nil || len(got) != 0 {
		t.Fatalf("nil S3 config must give an empty, non-nil summary, got %v", got)
	}
	if got := ConnectionSummary("other.plugin", map[string]any{"bucket": "b"}); got == nil || len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	if got := ConnectionSummary("bloem.storage.bookwarehouse", map[string]any{"base_url": "https://books.example/api", "api_key": "K"}); got["base_url_host"] != "books.example" || len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestConnectionSummaryS3OmitsUnsafeEndpoint(t *testing.T) {
	for name, endpoint := range map[string]string{
		"userinfo":          "https://user:pw@s3.example.com",
		"userinfo no pw":    "https://token@s3.example.com",
		"http":              "http://s3.example.com",
		"no scheme":         "s3.example.com",
		"unparseable":       "https://s3.example.com:badport/\x7f",
		"no host":           "https://",
		"other scheme":      "ftp://s3.example.com",
		"empty userinfo at": "https://@s3.example.com",
	} {
		got := ConnectionSummary("bloem.storage.s3", map[string]any{"bucket": "media", "endpoint": endpoint})
		if _, present := got["endpoint"]; present {
			t.Fatalf("%s: unsafe endpoint %q was summarized as %q", name, endpoint, got["endpoint"])
		}
		raw, _ := json.Marshal(got)
		if strings.Contains(string(raw), "pw") || strings.Contains(string(raw), "token@") {
			t.Fatalf("%s: summary leaked userinfo: %s", name, raw)
		}
		if got["bucket"] != "media" {
			t.Fatalf("%s: other fields must survive, got %v", name, got)
		}
	}
	if got := ConnectionSummary("bloem.storage.s3", map[string]any{"bucket": "media", "endpoint": "https://s3.example.com:9000"}); got["endpoint"] != "https://s3.example.com:9000" {
		t.Fatalf("valid https endpoint dropped: %v", got)
	}
}

// The S3 request limits and the proxy address and user are summarized so an
// edit form can keep them; the proxy password never is.
func TestConnectionSummaryS3LimitsAndProxy(t *testing.T) {
	// The config store decodes numbers as json.Number; a request as float64.
	conn := map[string]any{"bucket": "media", "auth_failure_budget": json.Number("3"), "auth_failure_window_seconds": json.Number("60"), "auth_pause_seconds": json.Number("600"),
		"auth_pause_max_seconds": json.Number("3600"), "max_concurrent_requests": 16.0, "max_listing_requests": json.Number("3"), "max_read_mbps": int64(90),
		"http_proxy": "http://proxy.example:3128", "http_proxy_username": "squid", "http_proxy_password": "PROXYSECRET"}
	got := ConnectionSummary("bloem.storage.s3", conn)
	want := map[string]string{"bucket": "media", "prefix": "", "auth_failure_budget": "3", "auth_failure_window_seconds": "60", "auth_pause_seconds": "600",
		"auth_pause_max_seconds": "3600", "max_concurrent_requests": "16", "max_listing_requests": "3", "max_read_mbps": "90",
		"http_proxy": "http://proxy.example:3128", "http_proxy_username": "squid"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %q want %q", k, got[k], v)
		}
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "PROXYSECRET") {
		t.Fatal("summary leaked the proxy password")
	}
	for name, value := range map[string]any{"fraction": 2.5, "zero": 0.0, "negative": -1.0, "text": "16", "bool": true, "number_fraction": json.Number("2.5"), "number_bad": json.Number("x")} {
		if got := ConnectionSummary("bloem.storage.s3", map[string]any{"bucket": "b", "max_read_mbps": value}); got["max_read_mbps"] != "" {
			t.Fatalf("%s: limit %v summarized as %q", name, value, got["max_read_mbps"])
		}
	}
	for name, proxy := range map[string]string{
		"userinfo": "http://u:pw@proxy.example:3128",
		"at":       "http://@proxy.example:3128",
		"scheme":   "socks5://proxy.example:1080",
		"no host":  "http://",
		"query":    "http://proxy.example:3128?x=1",
	} {
		if got := ConnectionSummary("bloem.storage.s3", map[string]any{"bucket": "b", "http_proxy": proxy}); got["http_proxy"] != "" {
			t.Fatalf("%s: unsafe proxy %q summarized", name, proxy)
		}
	}
	if got := ConnectionSummary("bloem.storage.s3", map[string]any{"bucket": "b", "http_proxy": "https://proxy.example/"}); got["http_proxy"] != "https://proxy.example/" {
		t.Fatalf("https proxy not summarized: %v", got)
	}
}
