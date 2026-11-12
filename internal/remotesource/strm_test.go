package remotesource

import (
	"errors"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/config"
)

func TestParseStrm(t *testing.T) {
	ok := map[string]string{
		"https://media.example.net/a.mkv":                     "https://media.example.net/a.mkv",
		"\n\n  http://media.example.net/a.mkv  \r\nignored\n": "http://media.example.net/a.mkv",
		"\xef\xbb\xbfhttps://h.example/x?token=abc&b=1":       "https://h.example/x?token=abc&b=1",
		"HTTPS://user:pw@h.example:8443/p%20q.mp4\n":          "HTTPS://user:pw@h.example:8443/p%20q.mp4",
		"http://10.0.0.5/admin":                               "http://10.0.0.5/admin", // parsing filters nothing; the guard decides
	}
	for in, want := range ok {
		got, err := ParseStrm([]byte(in))
		if err != nil || got != want {
			t.Errorf("ParseStrm(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{"", "\n \n", "ftp://h.example/x", "file:///etc/passwd", "/movies/x.mkv", "media.example.net/x", "http://", "rtsp://cam/x",
		"http://h.example/\x00x", strings.Repeat("a", MaxStrmBytes+1), "\xff\xfehttp://x"}
	for _, in := range bad {
		if _, err := ParseStrm([]byte(in)); !errors.Is(err, ErrInvalidStrm) {
			t.Errorf("ParseStrm(%q) = %v, want ErrInvalidStrm", in, err)
		}
	}
}

func TestAllowPrivateSettingKeyMatchesConfig(t *testing.T) {
	if AllowPrivateSettingKey != config.StrmAllowPrivateAddressesSettingKey {
		t.Fatalf("remotesource reads %q but the admin setting is %q", AllowPrivateSettingKey, config.StrmAllowPrivateAddressesSettingKey)
	}
	if normalized, err := config.NormalizeAdminSetting(AllowPrivateSettingKey, "TRUE"); err != nil || normalized != "true" {
		t.Fatalf("NormalizeAdminSetting = %q, %v", normalized, err)
	}
	if _, err := config.NormalizeAdminSetting(AllowPrivateSettingKey, "sometimes"); err == nil {
		t.Fatal("the setting must be a boolean")
	}
}

func TestIsStrmPathAndRevision(t *testing.T) {
	if !IsStrmPath("/movies/Harbor Lights (2019).STRM") || IsStrmPath("/movies/x.strm.mkv") || IsStrmPath("strm") {
		t.Fatal("IsStrmPath")
	}
	a, b := Revision("https://a.example/x"), Revision("https://a.example/y")
	if a == b || !strings.HasPrefix(a, "url-sha256:") || strings.Contains(a, "a.example") {
		t.Fatalf("Revision = %q, %q", a, b)
	}
	if NameHint("https://h.example/dir/Movie.m2ts?x=1") != "Movie.m2ts" || NameHint("https://h.example/") != "remote" {
		t.Fatal("NameHint")
	}
}
