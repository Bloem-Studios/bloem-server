package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

const (
	browseAccessKey    = "AKIAHANDLERBROWSE0001"
	browseSecretKey    = "handler/SECRET+browse+0001"
	browseSessionToken = "HANDLER-SESSION-browse-0001"
)

func assertNoBrowseCredentials(t *testing.T, label, text string) {
	t.Helper()
	for _, secret := range []string{browseAccessKey, browseSecretKey, browseSessionToken, "SECRET+browse", "SESSION-browse"} {
		if strings.Contains(text, secret) {
			t.Fatalf("%s leaked credential bytes", label)
		}
	}
}

type fakeS3BrowseCall struct {
	op     string
	opts   nativestorage.S3BrowseOptions
	conn   nativestorage.S3BrowseConnection
	bucket string
	prefix string
	cursor string
}

type fakeS3Browse struct {
	mu      sync.Mutex
	calls   []fakeS3BrowseCall
	buckets []nativestorage.S3Bucket
	page    nativestorage.S3FolderPage
	err     error
}

func (f *fakeS3Browse) ListBuckets(_ context.Context, opts nativestorage.S3BrowseOptions, conn nativestorage.S3BrowseConnection) ([]nativestorage.S3Bucket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeS3BrowseCall{op: "buckets", opts: opts, conn: conn})
	return f.buckets, f.err
}

func (f *fakeS3Browse) ListFolders(_ context.Context, opts nativestorage.S3BrowseOptions, conn nativestorage.S3BrowseConnection, bucket, prefix, cursor string) (nativestorage.S3FolderPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeS3BrowseCall{op: "folders", opts: opts, conn: conn, bucket: bucket, prefix: prefix, cursor: cursor})
	return f.page, f.err
}

func (f *fakeS3Browse) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

var (
	browseOrganizationID = uuid.MustParse("6a4b8f84-6f4e-4b55-9bd5-0d8d0b6b9a11")
	browsePlatformActor  = auth.AdminContextClaims{AccountID: 7, AccountIncarnationID: uuid.New(), SessionID: "fixture-session", Scope: auth.AdminScopePlatform}
	browseOrgActor       = auth.AdminContextClaims{AccountID: 8, AccountIncarnationID: uuid.New(), SessionID: "fixture-session", Scope: auth.AdminScopeOrganization, OrganizationID: browseOrganizationID, MembershipID: uuid.New()}
)

func browseRequest(t *testing.T, actor *auth.AdminContextClaims, path, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if actor == nil {
		return r
	}
	ctx := middleware.SetAdminContextClaims(r.Context(), *actor)
	if actor.Scope == auth.AdminScopeOrganization {
		ctx = tenancy.WithContext(ctx, tenancy.Context{AccountID: actor.AccountID, OrganizationID: actor.OrganizationID, MembershipID: actor.MembershipID})
	}
	return r.WithContext(ctx)
}

func browseHandler(browser NativeStorageS3Browser, scope auth.AdminScope) *BloemNativeStorageManagementHandler {
	h := NewBloemNativeStorageManagementHandler(nil)
	h.S3Browse = browser
	return h.ForAdminScope(scope)
}

func browseBody(extra string) string {
	body := `{"endpoint":"  https://s3.example.com  ","region":" us-east-1 ","access_key":" ` + browseAccessKey + ` ","secret_key":"` + browseSecretKey + `","session_token":"` + browseSessionToken + `"`
	if extra != "" {
		body += "," + extra
	}
	return body + "}"
}

func captureBrowseLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &logs
}

func TestS3BucketsHandlerBothScopes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		actor    auth.AdminContextClaims
		restrict bool
	}{
		{"platform", browsePlatformActor, false},
		{"organization", browseOrgActor, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeS3Browse{buckets: []nativestorage.S3Bucket{{Name: "media"}, {Name: "movies"}}}
			w := httptest.NewRecorder()
			browseHandler(fake, tc.actor.Scope).HandleS3Buckets(w, browseRequest(t, &tc.actor, "/s3/buckets", browseBody("")))
			if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status %d cache %q", w.Code, w.Header().Get("Cache-Control"))
			}
			if got := strings.TrimSpace(w.Body.String()); got != `{"buckets":[{"name":"media"},{"name":"movies"}]}` {
				t.Fatalf("body %s", got)
			}
			assertNoBrowseCredentials(t, "response", w.Body.String())
			if fake.callCount() != 1 {
				t.Fatalf("calls = %d", fake.callCount())
			}
			call := fake.calls[0]
			want := nativestorage.S3BrowseConnection{Endpoint: "https://s3.example.com", Region: "us-east-1", AccessKey: browseAccessKey, SecretKey: browseSecretKey, SessionToken: browseSessionToken}
			if call.conn != want {
				t.Fatal("connection fields were not trimmed and passed through")
			}
			if call.opts.RestrictPorts != tc.restrict || call.opts.Actor == "" {
				t.Fatalf("options = %+v", call.opts)
			}
		})
	}
}

func TestS3BucketsHandlerEmptyListIsArray(t *testing.T) {
	w := httptest.NewRecorder()
	browseHandler(&fakeS3Browse{}, auth.AdminScopePlatform).HandleS3Buckets(w, browseRequest(t, &browsePlatformActor, "/s3/buckets", browseBody("")))
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"buckets":[]}` {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestS3FoldersHandler(t *testing.T) {
	fake := &fakeS3Browse{page: nativestorage.S3FolderPage{Prefix: "movies/", Folders: []nativestorage.S3Folder{{Name: "10s", Prefix: "movies/10s/"}}, NextCursor: "tok"}}
	w := httptest.NewRecorder()
	browseHandler(fake, auth.AdminScopeOrganization).HandleS3Folders(w, browseRequest(t, &browseOrgActor, "/s3/folders", browseBody(`"bucket":" media ","prefix":"movies/","cursor":"tok0"`)))
	if w.Code != 200 {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"prefix":"movies/","folders":[{"name":"10s","prefix":"movies/10s/"}],"next_cursor":"tok"}` {
		t.Fatalf("body %s", got)
	}
	call := fake.calls[0]
	if call.bucket != "media" || call.prefix != "movies/" || call.cursor != "tok0" || !call.opts.RestrictPorts {
		t.Fatalf("call = bucket %q prefix %q cursor %q restrict %v", call.bucket, call.prefix, call.cursor, call.opts.RestrictPorts)
	}
	// An empty listing keeps folders an array and next_cursor a string.
	empty := &fakeS3Browse{page: nativestorage.S3FolderPage{}}
	w = httptest.NewRecorder()
	browseHandler(empty, auth.AdminScopePlatform).HandleS3Folders(w, browseRequest(t, &browsePlatformActor, "/s3/folders", browseBody(`"bucket":"media"`)))
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"prefix":"","folders":[],"next_cursor":""}` {
		t.Fatalf("empty page status %d body %s", w.Code, w.Body.String())
	}
}

func TestS3BrowseHandlerStrictRequest(t *testing.T) {
	full := map[string]string{"endpoint": `"https://s3.example.com"`, "region": `"us-east-1"`, "access_key": `"` + browseAccessKey + `"`, "secret_key": `"` + browseSecretKey + `"`}
	build := func(fields map[string]string, drop string, extra string) string {
		var parts []string
		for k, v := range fields {
			if k != drop {
				parts = append(parts, `"`+k+`":`+v)
			}
		}
		if extra != "" {
			parts = append(parts, extra)
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	cases := map[string]struct {
		path, body, query, contentType string
	}{
		"unknown_field":    {"/s3/buckets", build(full, "", `"bucket_owner":"x"`), "", ""},
		"bucket_on_list":   {"/s3/buckets", build(full, "", `"bucket":"media"`), "", ""},
		"duplicate_key":    {"/s3/buckets", build(full, "", `"region":"eu-west-1"`), "", ""},
		"missing_endpoint": {"/s3/buckets", build(full, "endpoint", ""), "", ""},
		"missing_region":   {"/s3/buckets", build(full, "region", ""), "", ""},
		"missing_access":   {"/s3/buckets", build(full, "access_key", ""), "", ""},
		"missing_secret":   {"/s3/buckets", build(full, "secret_key", ""), "", ""},
		"blank_secret":     {"/s3/buckets", build(full, "secret_key", `"secret_key":"   "`), "", ""},
		"null_region":      {"/s3/buckets", build(full, "region", `"region":null`), "", ""},
		"wrong_type":       {"/s3/buckets", build(full, "region", `"region":1`), "", ""},
		"bad_region":       {"/s3/buckets", build(full, "region", `"region":"US East"`), "", ""},
		"control_key":      {"/s3/buckets", build(full, "access_key", `"access_key":"a\u0007b"`), "", ""},
		"trailing_json":    {"/s3/buckets", build(full, "", "") + `{}`, "", ""},
		"not_object":       {"/s3/buckets", `[]`, "", ""},
		"query":            {"/s3/buckets", build(full, "", ""), "?endpoint=x", ""},
		"text_plain":       {"/s3/buckets", build(full, "", ""), "", "text/plain"},
		"missing_bucket":   {"/s3/folders", build(full, "", ""), "", ""},
		"bad_bucket":       {"/s3/folders", build(full, "", `"bucket":"Bad_Bucket"`), "", ""},
		"prefix_no_slash":  {"/s3/folders", build(full, "", `"bucket":"media","prefix":"movies"`), "", ""},
		"prefix_dotdot":    {"/s3/folders", build(full, "", `"bucket":"media","prefix":"movies/../"`), "", ""},
		"prefix_backslash": {"/s3/folders", build(full, "", `"bucket":"media","prefix":"a\\b/"`), "", ""},
		"prefix_too_long":  {"/s3/folders", build(full, "", `"bucket":"media","prefix":"`+strings.Repeat("a", 1025)+`/"`), "", ""},
		"cursor_too_long":  {"/s3/folders", build(full, "", `"bucket":"media","cursor":"`+strings.Repeat("c", 4097)+`"`), "", ""},
		"folders_unknown":  {"/s3/folders", build(full, "", `"bucket":"media","delimiter":"/"`), "", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &fakeS3Browse{}
			h := browseHandler(fake, auth.AdminScopePlatform)
			r := browseRequest(t, &browsePlatformActor, tc.path+tc.query, tc.body)
			if tc.contentType != "" {
				r.Header.Set("Content-Type", tc.contentType)
			}
			w := httptest.NewRecorder()
			if tc.path == "/s3/buckets" {
				h.HandleS3Buckets(w, r)
			} else {
				h.HandleS3Folders(w, r)
			}
			assertNativeError(t, w, http.StatusBadRequest, "invalid_request")
			if fake.callCount() != 0 {
				t.Fatal("invalid request reached the browser")
			}
			assertNoBrowseCredentials(t, "response", w.Body.String())
		})
	}
	t.Run("oversized", func(t *testing.T) {
		fake := &fakeS3Browse{}
		w := httptest.NewRecorder()
		big := browseBody(`"padding":"` + strings.Repeat("p", nativeStorageJSONLimit) + `"`)
		browseHandler(fake, auth.AdminScopePlatform).HandleS3Buckets(w, browseRequest(t, &browsePlatformActor, "/s3/buckets", big))
		assertNativeError(t, w, http.StatusRequestEntityTooLarge, "request_too_large")
	})
}

func TestS3BrowseHandlerRequiresAdminContext(t *testing.T) {
	h := browseHandler(&fakeS3Browse{}, auth.AdminScopePlatform)
	for _, call := range []http.HandlerFunc{h.HandleS3Buckets, h.HandleS3Folders} {
		r := httptest.NewRequest(http.MethodPost, "/s3/buckets", nil)
		r.Body = nativeUnreadBody{t}
		w := httptest.NewRecorder()
		call(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous status %d", w.Code)
		}
	}
	// An organization administrator on the platform route has no platform authority.
	r := browseRequest(t, &browseOrgActor, "/s3/buckets", browseBody(""))
	r.Body = nativeUnreadBody{t}
	w := httptest.NewRecorder()
	h.HandleS3Buckets(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("organization actor on platform route: status %d", w.Code)
	}
}

func TestS3BrowseHandlerErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		code   string
		cause  error
		status int
	}{
		{nativestorage.S3EndpointRejectedCode, nil, 422},
		{nativestorage.S3AccessDeniedCode, nil, 422},
		{nativestorage.S3BackendErrorCode, nil, 502},
		{"native_storage_unavailable", nativestorage.ErrS3BrowseBusy, 503},
	} {
		t.Run(tc.code, func(t *testing.T) {
			fake := &fakeS3Browse{err: &catalog.NativeOnboardingError{Code: tc.code, Cause: tc.cause}}
			for _, path := range []string{"/s3/buckets", "/s3/folders"} {
				w := httptest.NewRecorder()
				h := browseHandler(fake, auth.AdminScopePlatform)
				if path == "/s3/buckets" {
					h.HandleS3Buckets(w, browseRequest(t, &browsePlatformActor, path, browseBody("")))
				} else {
					h.HandleS3Folders(w, browseRequest(t, &browsePlatformActor, path, browseBody(`"bucket":"media"`)))
				}
				body := assertNativeError(t, w, tc.status, tc.code)
				if len(body) != 2 {
					t.Fatalf("error body has extra fields: %v", body)
				}
				if tc.cause != nil && w.Header().Get("Retry-After") == "" {
					t.Fatal("busy refusal lacks Retry-After")
				}
			}
		})
	}
	for code, message := range map[string]string{
		nativestorage.S3EndpointRejectedCode: "The S3 endpoint is not allowed",
		nativestorage.S3AccessDeniedCode:     "The S3 backend denied access",
		nativestorage.S3BackendErrorCode:     "The S3 backend request failed",
	} {
		if got := NativeStorageAPIError(&catalog.NativeOnboardingError{Code: code}); got.Code != code || got.Message != message {
			t.Fatalf("%s maps to %+v", code, got)
		}
	}
}

// The real browser refuses blocked endpoints and, for organization
// administrators, non-S3 ports, before any DNS or network I/O.
func TestS3BrowseHandlerRealBrowserRefusesEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name     string
		actor    auth.AdminContextClaims
		endpoint string
	}{
		{"loopback_literal", browsePlatformActor, "https://127.0.0.1"},
		{"metadata_literal", browseOrgActor, "https://169.254.169.254"},
		{"mapped_private_literal", browsePlatformActor, "https://[::ffff:10.0.0.1]"},
		{"http", browsePlatformActor, "http://s3.example.com"},
		{"path", browsePlatformActor, "https://s3.example.com/bucket"},
		{"userinfo", browseOrgActor, "https://u:p@s3.example.com"},
		{"organization_port", browseOrgActor, "https://s3.example.com:6379"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			body := strings.Replace(browseBody(""), "  https://s3.example.com  ", tc.endpoint, 1)
			browseHandler(nativestorage.NewS3Browser(), tc.actor.Scope).HandleS3Buckets(w, browseRequest(t, &tc.actor, "/s3/buckets", body))
			got := assertNativeError(t, w, 422, nativestorage.S3EndpointRejectedCode)
			if got["message"] != "The S3 endpoint is not allowed" {
				t.Fatalf("message %v", got["message"])
			}
		})
	}
}

func TestS3BrowseHandlerUnavailableWithoutBrowser(t *testing.T) {
	w := httptest.NewRecorder()
	browseHandler(nil, auth.AdminScopePlatform).HandleS3Buckets(w, browseRequest(t, &browsePlatformActor, "/s3/buckets", browseBody("")))
	assertNativeError(t, w, http.StatusServiceUnavailable, "native_storage_unavailable")
}

// Neither the request logger nor the browse audit line may capture any
// credential; the audit line names actor, scope, host, bucket and outcome.
func TestS3BrowseHandlerLoggingIsCredentialFree(t *testing.T) {
	logs := captureBrowseLogs(t)
	for _, tc := range []struct {
		name    string
		actor   auth.AdminContextClaims
		path    string
		extra   string
		err     error
		outcome string
	}{
		{"ok", browseOrgActor, "/s3/folders", `"bucket":"media","prefix":"movies/"`, nil, "ok"},
		{"denied", browsePlatformActor, "/s3/buckets", "", &catalog.NativeOnboardingError{Code: nativestorage.S3AccessDeniedCode}, nativestorage.S3AccessDeniedCode},
		{"invalid", browsePlatformActor, "/s3/folders", `"bucket":"media","prefix":"bad"`, nil, "invalid_request"},
	} {
		logs.Reset()
		fake := &fakeS3Browse{err: tc.err}
		h := browseHandler(fake, tc.actor.Scope)
		call := h.HandleS3Buckets
		if tc.path == "/s3/folders" {
			call = h.HandleS3Folders
		}
		w := httptest.NewRecorder()
		middleware.RequestLogger("node-test")(http.HandlerFunc(call)).ServeHTTP(w, browseRequest(t, &tc.actor, tc.path, browseBody(tc.extra)))
		text := logs.String()
		assertNoBrowseCredentials(t, tc.name+" logs", text)
		assertNoBrowseCredentials(t, tc.name+" response", w.Body.String())
		var audit map[string]any
		for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
			var entry map[string]any
			if json.Unmarshal([]byte(line), &entry) == nil && entry["msg"] == "native storage s3 browse" {
				audit = entry
			}
		}
		if audit == nil {
			t.Fatalf("%s: no audit line in %s", tc.name, text)
		}
		if audit["outcome"] != tc.outcome || audit["scope"] != string(tc.actor.Scope) || audit["account_id"] != float64(tc.actor.AccountID) || audit["endpoint_host"] != "s3.example.com" {
			t.Fatalf("%s: audit = %v", tc.name, audit)
		}
		if tc.path == "/s3/folders" && tc.outcome == "ok" && (audit["bucket"] != "media" || audit["organization_id"] != browseOrganizationID.String()) {
			t.Fatalf("%s: audit = %v", tc.name, audit)
		}
		if !strings.Contains(text, `"msg":"api request"`) {
			t.Fatalf("%s: request logger did not run", tc.name)
		}
	}
}

type browseCapabilityFixture struct{ ready bool }

func (f browseCapabilityFixture) NativeStorageReady(context.Context) bool { return f.ready }

func TestS3BucketBrowseCapability(t *testing.T) {
	for _, tc := range []struct {
		name    string
		scope   auth.AdminScope
		ready   bool
		browser NativeStorageS3Browser
		want    bool
	}{
		{"platform_composed", auth.AdminScopePlatform, true, &fakeS3Browse{}, true},
		{"organization_composed", auth.AdminScopeOrganization, true, &fakeS3Browse{}, true},
		{"without_browser", auth.AdminScopePlatform, true, nil, false},
		{"not_ready", auth.AdminScopeOrganization, false, &fakeS3Browse{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &BloemNativeStorageManagementHandler{Capabilities: browseCapabilityFixture{tc.ready}, S3Browse: tc.browser}
			if got := h.ForAdminScope(tc.scope).nativeStorageCapabilities(t.Context())["s3_bucket_browse"]; got != tc.want {
				t.Fatalf("s3_bucket_browse = %v, want %v", got, tc.want)
			}
		})
	}
}
