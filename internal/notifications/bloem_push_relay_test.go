package notifications

// Bloem-owned tests for the two push relays (bloem_push_relay.go). Two fake
// relays stand in for Silo's and Bloem's; the client trusts both, so the only
// thing keeping a request on its own relay is the server's routing.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/serversettings"
)

const (
	siloAppAPNsToken  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	siloAppFCMToken   = "silo-app-fcm:token-0123456789-0123456789-0123456789-0123456789-0123"
	adminAppAPNsToken = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// fakeCapability is a relay capability in the relays' compact JWS shape. Only
// the issuer claim matters here; nothing verifies the signature.
func fakeCapability(issuer, subject string) string {
	enc := func(v any) string {
		raw, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	return enc(map[string]string{"alg": "EdDSA", "typ": "silo-relay-capability+jwt"}) + "." +
		enc(map[string]string{"iss": issuer, "sub": subject}) + ".c2lnbmF0dXJl"
}

type twoRelayCall struct {
	Host          string
	Path          string
	Authorization string
	Body          string
}

// fakePushRelay is one relay: it registers a deployment with a capability
// from its own issuer and accepts every send.
type fakePushRelay struct {
	server *httptest.Server
	// origin is the production origin this fake answers for.
	origin string

	mu    sync.Mutex
	calls []twoRelayCall
}

func newFakePushRelay(t *testing.T, origin, deployment string) *fakePushRelay {
	t.Helper()
	relay := &fakePushRelay{origin: origin}
	relay.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		relay.mu.Lock()
		relay.calls = append(relay.calls, twoRelayCall{Host: r.Host, Path: r.URL.Path, Authorization: r.Header.Get("Authorization"), Body: string(raw)})
		relay.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case relayRegisterPath, relayRenewPath, relayRotatePath:
			// Each relay issues capabilities under its own origin.
			_, _ = w.Write([]byte(credentialJSON(deployment, fakeCapability(origin, deployment), time.Now().Add(30*24*time.Hour))))
		default:
			_, _ = w.Write([]byte(`{"request_id":"relay-request","status":"accepted"}`))
		}
	}))
	t.Cleanup(relay.server.Close)
	return relay
}

func (r *fakePushRelay) seen() []twoRelayCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]twoRelayCall(nil), r.calls...)
}

// twoRelayTransport answers each fake's production origin with that fake and
// refuses every other host, so the senders run with their production relay
// origins and no test here can reach a real relay.
func twoRelayTransport(t *testing.T, relays ...*fakePushRelay) http.RoundTripper {
	t.Helper()
	pool := x509.NewCertPool()
	fakeHost := map[string]string{}
	for _, relay := range relays {
		pool.AddCert(relay.server.Certificate())
		fakeHost[strings.TrimPrefix(relay.origin, "https://")] = strings.TrimPrefix(relay.server.URL, "https://")
	}
	base := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	return relayRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		host, ok := fakeHost[req.URL.Host]
		if !ok || req.URL.Scheme != "https" {
			if req.Body != nil {
				_ = req.Body.Close()
			}
			return nil, fmt.Errorf("test transport refuses %s://%s", req.URL.Scheme, req.URL.Host)
		}
		clone := req.Clone(req.Context())
		clone.URL.Host = host
		clone.Host = req.URL.Host
		return base.RoundTrip(clone)
	})
}

// newRelayStore is one shared settings table, as server_settings is.
func newRelayStore(values map[string]string) *atomicRelaySettings {
	if values == nil {
		values = map[string]string{}
	}
	return &atomicRelaySettings{lockedRelaySettings: lockedRelaySettings{values: values}}
}

// twoRelayServer is a Bloem server's two senders over one settings table.
type twoRelayServer struct {
	store *atomicRelaySettings
	silo  *pushSender
	admin *AdminPushService
}

func newTwoRelayServer(t *testing.T, store *atomicRelaySettings, siloRelay, bloemRelay *fakePushRelay) *twoRelayServer {
	t.Helper()
	transport := twoRelayTransport(t, siloRelay, bloemRelay)
	settings := NewSettings(store)
	silo := newPushSender(nil, nil, nil, settings)
	silo.client = &http.Client{Transport: transport}
	silo.developmentRelayURL = ""
	bloem := newBloemRelaySenderFor(nil, store, "", transport)
	return &twoRelayServer{
		store: store,
		silo:  silo,
		admin: &AdminPushService{sender: bloem, settings: settings, now: time.Now},
	}
}

func (s *twoRelayServer) sendSiloAppPush(t *testing.T, device *PushDevice, token string) pushSendResult {
	t.Helper()
	return s.silo.send(context.Background(), PushDeliveryAttempt{ID: "attempt-" + device.ID}, device, token)
}

func (s *twoRelayServer) sendAdminPush(t *testing.T) pushSendResult {
	t.Helper()
	result, _ := s.admin.relaySend(context.Background(), relayAppleSendPath, adminRelayApple{
		Token: adminAppAPNsToken, Environment: APNsEnvironmentProd, Topic: AdminPushAppleTopic,
		Mode: relayModePrivateAlert, ServerDeviceID: "admin-device", DeliveryID: "admin-device.delivery-1",
	}, "admin-attempt-1", PushPlatformApple)
	return result
}

func siloAppleDevice() *PushDevice {
	return &PushDevice{ID: "apple-1", Platform: PushPlatformApple, APNsEnvironment: APNsEnvironmentProd,
		APNsTopic: ApplePushTopicSilo, ServerDeviceID: "silo-apple", ProfileID: "profile-1", PushMode: PushModePrivatePush, Enabled: true}
}

func siloAndroidDevice() *PushDevice {
	return &PushDevice{ID: "android-1", Platform: PushPlatformAndroid, ServerDeviceID: "silo-android",
		ProfileID: "profile-1", PushMode: PushModePrivatePush, Enabled: true}
}

func paths(calls []twoRelayCall) []string {
	out := make([]string, 0, len(calls))
	for _, call := range calls {
		out = append(out, call.Path)
	}
	return out
}

// noCall fails when any request to relay mentions one of the values.
func noCall(t *testing.T, name string, relay *fakePushRelay, values ...string) {
	t.Helper()
	for _, call := range relay.seen() {
		for _, value := range values {
			if value != "" && (strings.Contains(call.Body, value) || strings.Contains(call.Authorization, value)) {
				t.Errorf("%s relay received %q in %s", name, value, call.Path)
			}
		}
	}
}

// A Silo app's device is delivered through Silo's relay and the Bloem Admin
// app's through Bloem's, each with its own credential, and nothing of either
// reaches the other relay.
func TestPushRoutesEachAppToItsOwnRelay(t *testing.T) {
	siloRelay := newFakePushRelay(t, DefaultPushRelayURL, "silo-deployment")
	bloemRelay := newFakePushRelay(t, BloemPushRelayURL, "bloem-deployment")
	server := newTwoRelayServer(t, newRelayStore(nil), siloRelay, bloemRelay)

	if result := server.sendSiloAppPush(t, siloAppleDevice(), siloAppAPNsToken); !result.OK {
		t.Fatalf("Silo app Apple push: %+v", result)
	}
	if result := server.sendSiloAppPush(t, siloAndroidDevice(), siloAppFCMToken); !result.OK {
		t.Fatalf("Silo app Android push: %+v", result)
	}
	if result := server.sendAdminPush(t); !result.OK {
		t.Fatalf("admin app push: %+v", result)
	}

	if got := paths(siloRelay.seen()); strings.Join(got, ",") != relayRegisterPath+","+relayAppleSendPath+","+relayFcmSendPath {
		t.Fatalf("Silo relay saw %v, want register then the two Silo-app sends", got)
	}
	if got := paths(bloemRelay.seen()); strings.Join(got, ",") != relayRegisterPath+","+relayAppleSendPath {
		t.Fatalf("Bloem relay saw %v, want register then the admin send", got)
	}

	var siloSend struct {
		Token, Topic, Mode string
	}
	_ = json.Unmarshal([]byte(siloRelay.seen()[1].Body), &siloSend)
	if siloSend.Token != siloAppAPNsToken || siloSend.Topic != ApplePushTopicSilo || siloSend.Mode != relayModePrivateAlert {
		t.Fatalf("Silo relay Apple send = %+v", siloSend)
	}

	siloCapability := server.store.values[SettingPushRelayAPIKey]
	bloemCapability := server.store.values[BloemSettingPushRelayAPIKey]
	if capabilityIssuer(siloCapability) != DefaultPushRelayURL || capabilityIssuer(bloemCapability) != BloemPushRelayURL {
		t.Fatalf("credentials: Silo keys hold %q, Bloem keys hold %q", capabilityIssuer(siloCapability), capabilityIssuer(bloemCapability))
	}
	noCall(t, "Silo", siloRelay, adminAppAPNsToken, AdminPushAppleTopic, bloemCapability)
	noCall(t, "Bloem", bloemRelay, siloAppAPNsToken, siloAppFCMToken, ApplePushTopicSilo, siloCapability)
}

// The server registers with Silo's relay only when a Silo-app device is
// first delivered to: Bloem's own pushes never contact it.
func TestSiloRelayRegistersOnlyForASiloAppDevice(t *testing.T) {
	siloRelay := newFakePushRelay(t, DefaultPushRelayURL, "silo-deployment")
	bloemRelay := newFakePushRelay(t, BloemPushRelayURL, "bloem-deployment")
	server := newTwoRelayServer(t, newRelayStore(nil), siloRelay, bloemRelay)

	for range 3 {
		if result := server.sendAdminPush(t); !result.OK {
			t.Fatalf("admin app push: %+v", result)
		}
	}
	if moved, err := server.admin.separateRelayCredentials(context.Background()); err != nil || moved {
		t.Fatalf("separate on a clean table = %v, %v", moved, err)
	}
	if calls := siloRelay.seen(); len(calls) != 0 {
		t.Fatalf("Silo relay contacted before any Silo-app device: %v", paths(calls))
	}
	if server.store.values[SettingPushRelayAPIKey] != "" {
		t.Fatal("Silo relay credential stored before any Silo-app device")
	}

	if result := server.sendSiloAppPush(t, siloAppleDevice(), siloAppAPNsToken); !result.OK {
		t.Fatalf("Silo app push: %+v", result)
	}
	if got := paths(siloRelay.seen()); len(got) != 2 || got[0] != relayRegisterPath {
		t.Fatalf("Silo relay saw %v, want one registration then the send", got)
	}
	if server.store.values[SettingPushRelayDeploymentID] != "silo-deployment" {
		t.Fatalf("Silo deployment = %q", server.store.values[SettingPushRelayDeploymentID])
	}
}

// Bloem's sender refuses every origin but Bloem's relay, even when its stored
// credential names Silo's.
func TestBloemRelaySenderNeverReachesSiloRelay(t *testing.T) {
	siloRelay := newFakePushRelay(t, DefaultPushRelayURL, "silo-deployment")
	bloemRelay := newFakePushRelay(t, BloemPushRelayURL, "bloem-deployment")
	// Silo's NormalizePushRelayURL always allows Silo's origin, so only the
	// origin guard stands between this stored value and Silo's relay.
	store := newRelayStore(map[string]string{
		BloemSettingPushRelayURL:    DefaultPushRelayURL,
		BloemSettingPushRelayAPIKey: fakeCapability(BloemPushRelayURL, "bloem-deployment"),
	})
	server := newTwoRelayServer(t, store, siloRelay, bloemRelay)

	if result := server.sendAdminPush(t); result.OK {
		t.Fatalf("admin push through a Silo-relay credential succeeded: %+v", result)
	}
	if calls := siloRelay.seen(); len(calls) != 0 {
		t.Fatalf("Bloem's sender reached Silo's relay: %v", paths(calls))
	}
}

// After an upgrade the table holds Bloem's relay credential under Silo's
// names. It moves to Bloem's names, Silo's are left as a server that never
// registered, and the next Silo-app push registers with Silo's relay instead
// of carrying Bloem's capability there.
func TestUpgradeMovesBloemRelayCredentialOffSiloKeys(t *testing.T) {
	siloRelay := newFakePushRelay(t, DefaultPushRelayURL, "silo-deployment")
	bloemRelay := newFakePushRelay(t, BloemPushRelayURL, "bloem-deployment")
	old := fakeCapability(BloemPushRelayURL, "bloem-existing")
	expires := time.Now().Add(20 * 24 * time.Hour).UTC().Format(time.RFC3339)
	store := newRelayStore(map[string]string{
		SettingPushRelayURL:          BloemPushRelayURL,
		SettingPushRelayDeploymentID: "bloem-existing",
		SettingPushRelayAPIKey:       old,
		SettingPushRelayExpiresAt:    expires,
		SettingPushRelayKeyPrefix:    "cap_v1_old",
		SettingPushRelayReregister:   "false",
	})
	server := newTwoRelayServer(t, store, siloRelay, bloemRelay)

	moved, err := server.admin.separateRelayCredentials(context.Background())
	if err != nil || !moved {
		t.Fatalf("separate = %v, %v; want moved", moved, err)
	}
	want := map[string]string{
		BloemSettingPushRelayURL: BloemPushRelayURL, BloemSettingPushRelayDeploymentID: "bloem-existing",
		BloemSettingPushRelayAPIKey: old, BloemSettingPushRelayExpiresAt: expires, BloemSettingPushRelayKeyPrefix: "cap_v1_old",
		SettingPushRelayURL: "", SettingPushRelayDeploymentID: "", SettingPushRelayAPIKey: "", SettingPushRelayReregister: "false",
	}
	for key, value := range want {
		if store.values[key] != value {
			t.Errorf("%s = %q, want %q", key, store.values[key], value)
		}
	}
	if moved, err := server.admin.separateRelayCredentials(context.Background()); err != nil || moved {
		t.Fatalf("second separate = %v, %v; want nothing to do", moved, err)
	}

	if result := server.sendAdminPush(t); !result.OK {
		t.Fatalf("admin push: %+v", result)
	}
	if result := server.sendSiloAppPush(t, siloAppleDevice(), siloAppAPNsToken); !result.OK {
		t.Fatalf("Silo app push: %+v", result)
	}
	if got := paths(bloemRelay.seen()); strings.Join(got, ",") != relayAppleSendPath {
		t.Fatalf("Bloem relay saw %v, want the admin send on the kept credential", got)
	}
	if got := paths(siloRelay.seen()); strings.Join(got, ",") != relayRegisterPath+","+relayAppleSendPath {
		t.Fatalf("Silo relay saw %v, want a fresh registration, then the Silo-app send", got)
	}
	if call := siloRelay.seen()[0]; call.Authorization != "" {
		t.Fatalf("Silo relay registration carried %q, want no credential", call.Authorization)
	}
	if call := bloemRelay.seen()[0]; call.Authorization != "Bearer "+old {
		t.Fatalf("admin send used %q, want the moved capability", call.Authorization)
	}
	noCall(t, "Silo", siloRelay, old)
	noCall(t, "Bloem", bloemRelay, siloAppAPNsToken)
}

// What counts as Bloem's credential under Silo's names, and what the move
// leaves behind.
func TestSeparateRelayCredentialsCases(t *testing.T) {
	bloemCap := fakeCapability(BloemPushRelayURL, "bloem-existing")
	siloCap := fakeCapability("https://push.siloserver.org", "silo-existing")
	cases := []struct {
		name      string
		values    map[string]string
		wantMoved bool
		want      map[string]string
	}{
		{
			name:   "Silo's own credential stays",
			values: map[string]string{SettingPushRelayURL: DefaultPushRelayURL, SettingPushRelayAPIKey: siloCap},
			want:   map[string]string{SettingPushRelayURL: DefaultPushRelayURL, SettingPushRelayAPIKey: siloCap, BloemSettingPushRelayAPIKey: ""},
		},
		{
			name:      "production Bloem origin moves",
			values:    map[string]string{SettingPushRelayURL: BloemPushRelayURL + "/", SettingPushRelayAPIKey: bloemCap},
			wantMoved: true,
			want:      map[string]string{SettingPushRelayURL: "", SettingPushRelayAPIKey: "", BloemSettingPushRelayURL: BloemPushRelayURL + "/", BloemSettingPushRelayAPIKey: bloemCap},
		},
		{
			name:      "no origin but a Bloem-issued capability moves",
			values:    map[string]string{SettingPushRelayAPIKey: bloemCap},
			wantMoved: true,
			want:      map[string]string{SettingPushRelayAPIKey: "", BloemSettingPushRelayURL: BloemPushRelayURL, BloemSettingPushRelayAPIKey: bloemCap},
		},
		{
			name:   "no origin and a Silo-issued capability stays",
			values: map[string]string{SettingPushRelayAPIKey: siloCap},
			want:   map[string]string{SettingPushRelayAPIKey: siloCap, BloemSettingPushRelayAPIKey: ""},
		},
		{
			name:   "a legacy key stays for Silo's path to replace",
			values: map[string]string{SettingPushRelayAPIKey: "rk_legacy"},
			want:   map[string]string{SettingPushRelayAPIKey: "rk_legacy", BloemSettingPushRelayAPIKey: ""},
		},
		{
			name: "Bloem's names already hold a credential: Silo's copy is dropped",
			values: map[string]string{SettingPushRelayURL: BloemPushRelayURL, SettingPushRelayAPIKey: fakeCapability(BloemPushRelayURL, "duplicate"),
				BloemSettingPushRelayURL: BloemPushRelayURL, BloemSettingPushRelayAPIKey: bloemCap},
			wantMoved: true,
			want:      map[string]string{SettingPushRelayAPIKey: "", BloemSettingPushRelayAPIKey: bloemCap},
		},
		{
			name:      "an administrator's clear stays a clear on Silo's names",
			values:    map[string]string{SettingPushRelayURL: BloemPushRelayURL, SettingPushRelayReregister: "true"},
			wantMoved: true,
			want:      map[string]string{SettingPushRelayURL: "", SettingPushRelayReregister: "true", BloemSettingPushRelayAPIKey: "", BloemSettingPushRelayReregister: ""},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newRelayStore(tc.values)
			admin := &AdminPushService{settings: NewSettings(store), sender: newBloemRelaySenderFor(nil, store, "", nil), now: time.Now}
			moved, err := admin.separateRelayCredentials(context.Background())
			if err != nil || moved != tc.wantMoved {
				t.Fatalf("separate = %v, %v; want %v", moved, err, tc.wantMoved)
			}
			for key, value := range tc.want {
				if store.values[key] != value {
					t.Errorf("%s = %q, want %q", key, store.values[key], value)
				}
			}
		})
	}
}

// The Bloem relay's key is encrypted at rest like Silo's.
func TestBloemRelayAPIKeyIsEncryptedAtRest(t *testing.T) {
	if !serversettings.SensitiveSettingKeys[BloemSettingPushRelayAPIKey] {
		t.Fatalf("%s is not in SensitiveSettingKeys", BloemSettingPushRelayAPIKey)
	}
	if !serversettings.SensitiveSettingKeys[SettingPushRelayAPIKey] {
		t.Fatalf("%s is not in SensitiveSettingKeys", SettingPushRelayAPIKey)
	}
}

func TestRelayOriginGuardRefusesOtherOrigins(t *testing.T) {
	reached := false
	guard := relayOriginGuard{next: relayRoundTripFunc(func(*http.Request) (*http.Response, error) {
		reached = true
		return relayResponse(http.StatusOK, `{}`), nil
	}), allowed: bloemRelayOrigins("")}
	for _, raw := range []string{"https://push.siloserver.org/v1/apple/send", "http://push.bloem-studios.com/v1/apple/send", "https://push.bloem-studios.com.example/v1"} {
		req, _ := http.NewRequest(http.MethodPost, raw, strings.NewReader("{}"))
		resp, err := guard.RoundTrip(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if !errors.Is(err, errRelayOriginRefused) {
			t.Errorf("%s: err = %v, want refused", raw, err)
		}
	}
	if reached {
		t.Fatal("a refused request reached the transport")
	}
	req, _ := http.NewRequest(http.MethodPost, BloemPushRelayURL+relayAppleSendPath, strings.NewReader("{}"))
	resp, err := guard.RoundTrip(req)
	if err != nil || !reached {
		t.Fatalf("Bloem's relay refused: %v", err)
	}
	_ = resp.Body.Close()
}
