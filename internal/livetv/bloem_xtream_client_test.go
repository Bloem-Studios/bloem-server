package livetv

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type xtreamRoundTrip func(*http.Request) (*http.Response, error)

func (f xtreamRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func xtreamFixtureClient(t *testing.T) *xtreamClient {
	t.Helper()
	c, err := newXtreamClient("https://provider.invalid/portal", xtreamCredentials{Username: "fixture+user", Password: "fixture&password?#%"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func xtreamFixtureResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestBloemXtreamLiveProtocol(t *testing.T) {
	c := xtreamFixtureClient(t)
	metadataCalls := 0
	c.metadata.Transport = xtreamRoundTrip(func(r *http.Request) (*http.Response, error) {
		metadataCalls++
		q := r.URL.Query()
		if r.URL.Host != "provider.invalid" || r.Method != http.MethodGet || q.Get("username") != c.credentials.Username || q.Get("password") != c.credentials.Password {
			t.Fatal("incorrect provider request")
		}
		if r.URL.Path == "/portal/xmltv.php" {
			return xtreamFixtureResponse(200, `<tv/>`), nil
		}
		if r.URL.Path != "/portal/player_api.php" {
			t.Fatal("unexpected provider endpoint")
		}
		switch q.Get("action") {
		case "":
			return xtreamFixtureResponse(200, `{"user_info":{"auth":1,"status":"Active","max_connections":"2","allowed_output_formats":["ts"]}}`), nil
		case "get_live_streams":
			return xtreamFixtureResponse(200, `[{"stream_id":2147483648,"name":"News","stream_type":"live","epg_channel_id":"news.example","direct_source":"http://169.254.169.254/unsafe","stream_icon":"https://provider.invalid/secret-icon"},{"stream_id":"42","name":"Sports"}]`), nil
		default:
			t.Fatal("requested non-live content")
			return nil, errors.New("unexpected action")
		}
	})
	max, err := c.authenticate(t.Context())
	if err != nil || max != 2 {
		t.Fatalf("authenticate: max=%d err=%v", max, err)
	}
	channels, err := c.channels(t.Context())
	if err != nil || len(channels) != 2 || channels[0].ID != "2147483648" || channels[0].EPGChannelID != "news.example" {
		t.Fatalf("channels: %#v err=%v", channels, err)
	}
	streamCalls := 0
	packets := make([]byte, 3*188)
	packets[0], packets[188], packets[376] = 0x47, 0x47, 0x47
	c.stream.Transport = xtreamRoundTrip(func(r *http.Request) (*http.Response, error) {
		streamCalls++
		if r.URL.Host != "provider.invalid" || r.URL.Path != "/portal/live/"+c.credentials.Username+"/"+c.credentials.Password+"/2147483648.ts" || r.URL.RawQuery != "" || r.URL.Fragment != "" {
			t.Fatal("stream credentials were not isolated in escaped path segments")
		}
		return xtreamFixtureResponse(200, string(packets)), nil
	})
	body, err := c.openLive(t.Context(), string(channels[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(data) != string(packets) || streamCalls != 1 {
		t.Fatal("stream body did not round trip")
	}
	c.guide.Transport = c.metadata.Transport
	body, err = c.openEPG(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()
	if metadataCalls != 3 || c.stream.Timeout != 0 || c.guide.Timeout != 0 || c.metadata.Timeout <= 0 {
		t.Fatal("incorrect metadata/stream transport lifetime")
	}
}

func TestBloemXtreamRefusesCredentialRedirectsAndRedactsErrors(t *testing.T) {
	for _, location := range []string{"https://other.invalid/live", "http://169.254.169.254/latest/meta-data", "https://provider.invalid/another-path"} {
		t.Run(location, func(t *testing.T) {
			c := xtreamFixtureClient(t)
			calls := 0
			transport := xtreamRoundTrip(func(*http.Request) (*http.Response, error) {
				calls++
				resp := xtreamFixtureResponse(302, "private provider error "+c.credentials.Password)
				resp.Header.Set("Location", location)
				return resp, nil
			})
			c.metadata.Transport, c.stream.Transport, c.guide.Transport = transport, transport, transport
			_, err := c.authenticate(t.Context())
			if err == nil || calls != 1 || strings.Contains(err.Error(), c.credentials.Password) {
				t.Fatalf("metadata redirect was followed or exposed credentials: calls=%d err=%v", calls, err)
			}
			_, err = c.openLive(t.Context(), "42")
			if err == nil || calls != 2 || strings.Contains(err.Error(), c.credentials.Password) {
				t.Fatal("stream redirect was followed or exposed credentials")
			}
			_, err = c.openEPG(t.Context())
			if err == nil || calls != 3 || strings.Contains(err.Error(), c.credentials.Password) {
				t.Fatal("guide redirect was followed or exposed credentials")
			}
		})
	}
	c := xtreamFixtureClient(t)
	c.metadata.Transport = xtreamRoundTrip(func(r *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Get", URL: r.URL.String(), Err: errors.New(c.credentials.Password)}
	})
	_, err := c.authenticate(t.Context())
	var upstream *url.Error
	if err == nil || strings.Contains(err.Error(), c.credentials.Password) || errors.As(err, &upstream) {
		t.Fatal("credential-bearing provider error escaped redaction")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.authenticate(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestBloemXtreamRejectsUnsafeConfiguration(t *testing.T) {
	for _, base := range []string{"http://127.0.0.1", "http://169.254.169.254", "file:///private", "https://user:password@provider.invalid", "https://provider.invalid?password=secret", "https://provider.invalid#fragment", "https://provider.invalid/a/../b", "https://provider.invalid/%2e%2e"} {
		t.Run(base, func(t *testing.T) {
			if _, err := newXtreamClient(base, xtreamCredentials{Username: "user", Password: "secret"}); err == nil {
				t.Fatal("unsafe configuration admitted")
			}
		})
	}
	for _, secret := range []string{"", "..", "a/b", "a\\b", "a\r\nb", strings.Repeat("a", 513)} {
		if _, err := newXtreamClient("https://provider.invalid", xtreamCredentials{Username: "user", Password: secret}); err == nil {
			t.Fatal("unsafe credential path segment admitted")
		}
	}
}

func TestBloemXtreamRejectsInvalidProviderData(t *testing.T) {
	for _, body := range []string{
		`{"user_info":{"auth":0,"status":"Active","max_connections":"2"}}`,
		`{"user_info":{"auth":1,"status":"Expired","max_connections":"2"}}`,
		`{"user_info":{"auth":"1","status":"Active","max_connections":"-1"}}`,
		`{"user_info":{"auth":1,"status":"Active","max_connections":"1","allowed_output_formats":["m3u8"]}}`,
		`{"user_info":{"auth":true,"status":"Active","max_connections":"2"}}`,
	} {
		c := xtreamFixtureClient(t)
		c.metadata.Transport = xtreamRoundTrip(func(*http.Request) (*http.Response, error) { return xtreamFixtureResponse(200, body), nil })
		if _, err := c.authenticate(t.Context()); err == nil {
			t.Fatal("invalid account accepted")
		}
	}
	for _, body := range []string{
		`[{"stream_id":1,"name":"A"},{"stream_id":"001","name":"B"}]`,
		`[{"stream_id":-1,"name":"A"}]`,
		`[{"stream_id":1,"name":"A","stream_type":"movie"}]`,
		`[{"stream_id":1,"name":""}]`,
		`{"error":"do not expose provider messages"}`,
		strings.Repeat(" ", xtreamJSONLimit+1),
	} {
		c := xtreamFixtureClient(t)
		c.metadata.Transport = xtreamRoundTrip(func(*http.Request) (*http.Response, error) { return xtreamFixtureResponse(200, body), nil })
		if _, err := c.channels(t.Context()); err == nil {
			t.Fatal("invalid live listing accepted")
		}
	}
	c := xtreamFixtureClient(t)
	c.stream.Transport = xtreamRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid stream ID reached provider")
		return nil, nil
	})
	for _, id := range []string{"0", "-1", "../42", "1?password=x", "9223372036854775808"} {
		if _, err := c.openLive(t.Context(), id); err == nil {
			t.Fatal("invalid stream ID accepted")
		}
	}
}

// A large XMLTV document from a slow provider must be allowed to stream for
// as long as the guide sync permits. The whole-request metadata timeout would
// cut the body; the guide client keeps only dial/TLS/response-header limits.
func TestBloemXtreamGuideDownloadOutlivesMetadataTimeout(t *testing.T) {
	allowLoopbackMediaFetch(t)
	const headerTimeout = 100 * time.Millisecond
	slowBody := func(w http.ResponseWriter, chunks int) {
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "<tv>")
		flusher.Flush()
		for range chunks {
			time.Sleep(50 * time.Millisecond)
			_, _ = io.WriteString(w, " ")
			flusher.Flush()
		}
		_, _ = io.WriteString(w, "</tv>")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow-headers/xmltv.php":
			time.Sleep(3 * headerTimeout)
			_, _ = io.WriteString(w, "<tv/>")
		case "/redirect/xmltv.php":
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		case "/elsewhere":
			t.Error("guide redirect was followed")
		default:
			slowBody(w, 8) // ~400ms total, 4x the injected timeout
		}
	}))
	defer srv.Close()
	creds := xtreamCredentials{Username: "fixture", Password: "secret-password"}
	client := func(path string) *xtreamClient {
		t.Helper()
		c, err := newXtreamClient(srv.URL+path, creds)
		if err != nil {
			t.Fatal(err)
		}
		c.metadata.Timeout = headerTimeout
		c.guide = newXtreamGuideHTTPClient(headerTimeout)
		return c
	}

	// The regression: the metadata client's whole-request timeout covers the body.
	c := client("/portal")
	resp, err := c.get(t.Context(), c.metadata, c.endpoint("xmltv.php", nil))
	if err == nil {
		_, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("fixture does not exceed the metadata client timeout")
	}

	body, err := c.openEPG(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || !strings.HasPrefix(string(data), "<tv>") || !strings.HasSuffix(string(data), "</tv>") {
		t.Fatalf("slow guide body was cut: %v", err)
	}
	if c.guide.Timeout != 0 {
		t.Fatal("guide client has a whole-request timeout")
	}

	// The sync context deadline still bounds the body.
	ctx, cancel := context.WithTimeout(t.Context(), 2*headerTimeout)
	defer cancel()
	if body, err = c.openEPG(ctx); err == nil {
		_, err = io.ReadAll(body)
		_ = body.Close()
	}
	if err == nil {
		t.Fatal("guide download outlived its context deadline")
	}

	// Response headers stay bounded.
	if _, err := client("/slow-headers").openEPG(t.Context()); err == nil || strings.Contains(err.Error(), creds.Password) {
		t.Fatalf("slow response headers accepted or error leaked credentials: %v", err)
	}
	// Redirects stay refused.
	if _, err := client("/redirect").openEPG(t.Context()); err == nil || strings.Contains(err.Error(), creds.Password) {
		t.Fatalf("guide redirect accepted or error leaked credentials: %v", err)
	}
	// Outbound SSRF guard stays on the guide transport.
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://169.254.169.254/latest/meta-data", nil)
	if resp, err := c.guide.Do(req); err == nil || !strings.Contains(err.Error(), "metadata addresses are not allowed") {
		if resp != nil {
			_ = resp.Body.Close()
		}
		t.Fatalf("guide transport dialed a metadata address: %v", err)
	}
}
