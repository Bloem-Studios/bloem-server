package livetv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	TunerTypeXtream    = "xtream"
	xtreamJSONLimit    = 16 << 20
	xtreamChannelLimit = 20000
)

// Xtream credentials are server-only. Do not attach this value, its requests,
// upstream errors, or credential-bearing URLs to logs or client responses.
// The integration uses only reviewed live endpoints; direct_source, VOD and
// series URLs returned by providers are deliberately ignored.
type xtreamCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type xtreamClient struct {
	base        *url.URL
	credentials xtreamCredentials
	metadata    *http.Client
	stream      *http.Client
}

type xtreamScalar string

func (v *xtreamScalar) UnmarshalJSON(raw []byte) error {
	if len(raw) > 256 {
		return errors.New("xtream scalar exceeds limit")
	}
	if string(raw) == "null" {
		*v = ""
		return nil
	}
	var s string
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &s); err != nil {
			return errors.New("invalid Xtream scalar")
		}
	} else {
		var n json.Number
		if err := json.Unmarshal(raw, &n); err != nil {
			return errors.New("invalid Xtream scalar")
		}
		s = n.String()
	}
	*v = xtreamScalar(s)
	return nil
}

type xtreamAccount struct {
	User struct {
		Auth           xtreamScalar `json:"auth"`
		Status         string       `json:"status"`
		MaxConnections xtreamScalar `json:"max_connections"`
		Formats        []string     `json:"allowed_output_formats"`
	} `json:"user_info"`
}

type xtreamLiveChannel struct {
	ID           xtreamScalar `json:"stream_id"`
	Name         string       `json:"name"`
	EPGChannelID string       `json:"epg_channel_id"`
	StreamType   string       `json:"stream_type"`
}

func newXtreamClient(raw string, credentials xtreamCredentials) (*xtreamClient, error) {
	if len(raw) > 2048 {
		return nil, fmt.Errorf("%w: provider base URL exceeds limit", ErrInvalidArgument)
	}
	if err := ValidateMediaFetchURL(raw); err != nil {
		return nil, fmt.Errorf("%w: invalid provider base URL", ErrInvalidArgument)
	}
	base, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.User != nil {
		return nil, fmt.Errorf("%w: use the provider base URL without credentials, query or fragment", ErrInvalidArgument)
	}
	if base.RawPath != "" || strings.Contains(base.Path, "\\") {
		return nil, fmt.Errorf("%w: invalid provider base path", ErrInvalidArgument)
	}
	for _, part := range strings.Split(base.Path, "/") {
		if part == "." || part == ".." {
			return nil, fmt.Errorf("%w: invalid provider base path", ErrInvalidArgument)
		}
	}
	base.Path = strings.TrimRight(base.Path, "/")
	base.Host = strings.ToLower(base.Host)
	if (base.Scheme == "https" && base.Port() == "443") || (base.Scheme == "http" && base.Port() == "80") {
		base.Host = base.Hostname()
		if strings.Contains(base.Host, ":") {
			base.Host = "[" + base.Host + "]"
		}
	}
	for _, value := range []string{credentials.Username, credentials.Password} {
		if value == "" || len(value) > 512 || !utf8.ValidString(value) || value == "." || value == ".." || strings.ContainsAny(value, "/\\\x00\r\n") {
			return nil, fmt.Errorf("%w: invalid Xtream credentials", ErrInvalidArgument)
		}
	}
	metadata := NewMediaHTTPClient()
	stream := NewStreamHTTPClient()
	// Provider credentials appear in both queries and paths. Never forward
	// them to a redirect target, including another path on the same host.
	// Providers that require redirects need an explicit reviewed delivery
	// integration, not automatic credential or SSRF trust expansion.
	redirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	metadata.CheckRedirect, stream.CheckRedirect = redirect, redirect
	return &xtreamClient{base: base, credentials: credentials, metadata: metadata, stream: stream}, nil
}

func (c *xtreamClient) endpoint(name string, values url.Values) string {
	u := *c.base
	u.Path += "/" + name
	if values == nil {
		values = url.Values{}
	}
	values.Set("username", c.credentials.Username)
	values.Set("password", c.credentials.Password)
	u.RawQuery = values.Encode()
	return u.String()
}

func (c *xtreamClient) get(ctx context.Context, client *http.Client, target string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, errors.New("xtream request could not be constructed")
	}
	resp, err := client.Do(req)
	if err != nil {
		// In particular, do not wrap url.Error: its URL contains the password.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Xtream provider request failed")
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("Xtream provider returned HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func (c *xtreamClient) readJSON(ctx context.Context, action string, target any) error {
	q := url.Values{}
	if action != "" {
		q.Set("action", action)
	}
	resp, err := c.get(ctx, c.metadata, c.endpoint("player_api.php", q))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, xtreamJSONLimit+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Xtream provider response could not be read")
	}
	if len(body) > xtreamJSONLimit {
		return errors.New("Xtream provider response exceeds limit")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return errors.New("Xtream provider returned invalid JSON")
	}
	return nil
}

func (c *xtreamClient) authenticate(ctx context.Context) (int, error) {
	var account xtreamAccount
	if err := c.readJSON(ctx, "", &account); err != nil {
		return 0, err
	}
	if account.User.Auth != "1" || !strings.EqualFold(account.User.Status, "Active") {
		return 0, errors.New("Xtream account is not active or authentication was refused")
	}
	if len(account.User.Formats) > 0 {
		ts := false
		for _, format := range account.User.Formats {
			ts = ts || strings.EqualFold(format, "ts")
		}
		if !ts {
			return 0, errors.New("Xtream provider must support MPEG-TS live output")
		}
	}
	max, err := strconv.Atoi(string(account.User.MaxConnections))
	if err != nil || max < 0 || max > 1024 {
		return 0, errors.New("Xtream provider returned an invalid connection limit")
	}
	return max, nil // zero is provider-declared unlimited; local admission still caps it.
}

func (c *xtreamClient) channels(ctx context.Context) ([]xtreamLiveChannel, error) {
	var channels []xtreamLiveChannel
	if err := c.readJSON(ctx, "get_live_streams", &channels); err != nil {
		return nil, err
	}
	if len(channels) > xtreamChannelLimit {
		return nil, errors.New("Xtream channel list exceeds limit")
	}
	seen := make(map[string]bool, len(channels))
	for i := range channels {
		ch := &channels[i]
		id, err := strconv.ParseInt(string(ch.ID), 10, 64)
		if err != nil || id <= 0 || (ch.StreamType != "" && ch.StreamType != "live") {
			return nil, errors.New("Xtream provider returned an invalid live channel")
		}
		ch.ID = xtreamScalar(strconv.FormatInt(id, 10))
		if seen[string(ch.ID)] {
			return nil, errors.New("Xtream provider returned duplicate live channel IDs")
		}
		seen[string(ch.ID)] = true
		ch.Name = strings.TrimSpace(ch.Name)
		if ch.Name == "" || len(ch.Name) > 512 || len(ch.EPGChannelID) > 512 {
			return nil, errors.New("Xtream channel metadata is invalid")
		}
	}
	return channels, nil
}

func (c *xtreamClient) openLive(ctx context.Context, id string) (io.ReadCloser, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("%w: invalid Xtream stream ID", ErrInvalidArgument)
	}
	// Parse escaped segments rather than concatenating raw credentials into a
	// URL path. This address never leaves the protected server-side HTTP client.
	target := c.base.String() + "/live/" + url.PathEscape(c.credentials.Username) + "/" + url.PathEscape(c.credentials.Password) + "/" + strconv.FormatInt(n, 10) + ".ts"
	streamCtx, cancel := context.WithCancel(ctx)
	startup := time.AfterFunc(mediaFetchTimeout, cancel)
	resp, err := c.get(streamCtx, c.stream, target)
	if err != nil {
		startup.Stop()
		cancel()
		return nil, err
	}
	// Some providers return a 200 login/error page containing the credentials.
	// Refuse clear-text/playlist responses and require MPEG-TS synchronization
	// before any bytes can reach a raw client or an encoder. Bound startup,
	// not the lifetime of an established live stream.
	kind := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	prefix := make([]byte, 3*188)
	invalidKind := strings.HasPrefix(kind, "text/") || kind == "application/json" || strings.Contains(kind, "mpegurl")
	var prefixErr error
	if !invalidKind {
		_, prefixErr = io.ReadFull(resp.Body, prefix)
	}
	stopped := startup.Stop()
	if invalidKind || prefixErr != nil || !stopped || streamCtx.Err() != nil || prefix[0] != 0x47 || prefix[188] != 0x47 || prefix[376] != 0x47 {
		cancel()
		_ = resp.Body.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Xtream provider did not return a usable MPEG-TS stream")
	}
	return &xtreamResponseBody{Reader: io.MultiReader(bytes.NewReader(prefix), resp.Body), body: resp.Body, cancel: cancel}, nil
}

type xtreamResponseBody struct {
	io.Reader
	body   io.ReadCloser
	cancel context.CancelFunc
}

func (b *xtreamResponseBody) Close() error {
	b.cancel()
	return b.body.Close()
}

func (c *xtreamClient) openEPG(ctx context.Context) (io.ReadCloser, error) {
	resp, err := c.get(ctx, c.metadata, c.endpoint("xmltv.php", nil))
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}
