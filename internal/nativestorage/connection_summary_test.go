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
