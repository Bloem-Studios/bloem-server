package notifications

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// bloemRelayFixture is a server whose Silo settings hold Silo's credential and
// whose Bloem settings hold Bloem's, with the server's signing secret set, over
// two fake relays that answer for the production origins.
func bloemRelayFixture(t *testing.T) (*twoRelayServer, *fakePushRelay, *fakePushRelay, map[string]string) {
	t.Helper()
	siloRelay := newFakePushRelay(t, DefaultPushRelayURL, "silo-deployment")
	bloemRelay := newFakePushRelay(t, BloemPushRelayURL, "bloem-deployment")
	expires := time.Now().Add(20 * 24 * time.Hour).UTC().Format(time.RFC3339)
	silo := map[string]string{
		SettingPushRelayURL:          DefaultPushRelayURL,
		SettingPushRelayDeploymentID: "silo-deployment",
		SettingPushRelayAPIKey:       fakeCapability(DefaultPushRelayURL, "silo-deployment"),
		SettingPushRelayExpiresAt:    expires,
		SettingPushRelayKeyPrefix:    "cap_v1_silo",
		SettingPushRelayReregister:   "false",
	}
	values := map[string]string{
		relayRotationSecretSetting:        "server-signing-secret",
		BloemSettingPushRelayURL:          BloemPushRelayURL,
		BloemSettingPushRelayDeploymentID: "bloem-deployment",
		BloemSettingPushRelayAPIKey:       fakeCapability(BloemPushRelayURL, "bloem-deployment"),
		BloemSettingPushRelayExpiresAt:    expires,
		BloemSettingPushRelayKeyPrefix:    "cap_v1_old",
		BloemSettingPushRelayReregister:   "false",
	}
	for key, value := range silo {
		values[key] = value
	}
	return newTwoRelayServer(t, newRelayStore(values), siloRelay, bloemRelay), siloRelay, bloemRelay, silo
}

// siloCredentialUnchanged fails when Silo's relay credential moved.
func siloCredentialUnchanged(t *testing.T, server *twoRelayServer, silo map[string]string) {
	t.Helper()
	for key, value := range silo {
		if got := server.store.values[key]; got != value {
			t.Errorf("Silo setting %s = %q, want it untouched (%q)", key, got, value)
		}
	}
}

// Bloem's relay disabling this deployment parks Bloem's credential: the admin
// app's pushes stop and nothing registers again on its own, as Silo's sender
// does for Silo's relay. An administrator's register signs the server up with
// Bloem's relay again, stores the new credential under Bloem's settings and
// pushes resume. Silo's relay is never contacted and Silo's credential stays.
func TestRegisterBloemRelayResumesAfterTheRelayDisabledThisDeployment(t *testing.T) {
	server, siloRelay, bloemRelay, silo := bloemRelayFixture(t)
	ctx := context.Background()

	bloemRelay.refuse(relayAppleSendPath, http.StatusUnauthorized, relayCodeDeploymentDisabled)
	if result := server.sendAdminPush(t); result.OK {
		t.Fatalf("admin push to a disabled deployment = %+v, want refused", result)
	}
	if key, parked := server.store.values[BloemSettingPushRelayAPIKey], server.store.values[BloemSettingPushRelayReregister]; key != "" || parked != "true" {
		t.Fatalf("after deployment_disabled the Bloem credential is key=%q reregistration_required=%q, want parked", key, parked)
	}
	bloemRelay.refuse(relayAppleSendPath, 0, "")
	before := len(bloemRelay.seen())
	if result := server.sendAdminPush(t); result.OK || len(bloemRelay.seen()) != before {
		t.Fatalf("a parked credential sent (%+v, %d relay calls), want no send until an administrator registers", result, len(bloemRelay.seen())-before)
	}

	result, err := server.admin.RegisterBloemRelay(ctx)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if result.Credential.DeploymentID != "bloem-deployment" || result.Credential.RelayURL != BloemPushRelayURL || result.Credential.APIKey == "" {
		t.Fatalf("register result = %+v, want Bloem's relay's new credential", result.Credential)
	}
	if got := paths(bloemRelay.seen()[before:]); strings.Join(got, ",") != relayRegisterPath {
		t.Fatalf("Bloem relay saw %v after the parked send, want one registration", got)
	}
	if call := bloemRelay.seen()[before]; call.Authorization != "" {
		t.Fatalf("registration carried %q, want no credential", call.Authorization)
	}
	if server.store.values[BloemSettingPushRelayAPIKey] != result.Credential.APIKey || server.store.values[BloemSettingPushRelayReregister] != "false" {
		t.Fatalf("Bloem settings after register: key=%q reregistration_required=%q",
			server.store.values[BloemSettingPushRelayAPIKey], server.store.values[BloemSettingPushRelayReregister])
	}
	if result := server.sendAdminPush(t); !result.OK {
		t.Fatalf("admin push after register = %+v, want accepted", result)
	}
	if calls := siloRelay.seen(); len(calls) != 0 {
		t.Fatalf("Silo relay saw %v, want nothing", paths(calls))
	}
	siloCredentialUnchanged(t, server, silo)
}

// A working credential is rotated, as Silo's register does for Silo's: the
// same deployment, a new capability, Bloem's settings only.
func TestRegisterBloemRelayRotatesAWorkingCredential(t *testing.T) {
	server, siloRelay, bloemRelay, silo := bloemRelayFixture(t)
	old := server.store.values[BloemSettingPushRelayAPIKey]

	result, err := server.admin.RegisterBloemRelay(context.Background())
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	calls := bloemRelay.seen()
	if got := paths(calls); strings.Join(got, ",") != relayRotatePath {
		t.Fatalf("Bloem relay saw %v, want one rotation", got)
	}
	if calls[0].Authorization != "Bearer "+old {
		t.Fatalf("rotation carried %q, want the stored capability", calls[0].Authorization)
	}
	if result.Credential.DeploymentID != "bloem-deployment" || server.store.values[BloemSettingPushRelayKeyPrefix] != "cap_v1_test" {
		t.Fatalf("after rotation: result %+v, stored key prefix %q", result.Credential, server.store.values[BloemSettingPushRelayKeyPrefix])
	}
	if len(siloRelay.seen()) != 0 {
		t.Fatalf("Silo relay saw %v, want nothing", paths(siloRelay.seen()))
	}
	siloCredentialUnchanged(t, server, silo)
}

// A relay that no longer accepts the stored identity answers the rotation 401;
// the administrator asked for a working credential, so a fresh deployment is
// registered instead, as on Silo.
func TestRegisterBloemRelayRegistersWhenRotationIsRejected(t *testing.T) {
	server, _, bloemRelay, silo := bloemRelayFixture(t)
	bloemRelay.refuse(relayRotatePath, http.StatusUnauthorized, "invalid_token")

	if _, err := server.admin.RegisterBloemRelay(context.Background()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := paths(bloemRelay.seen()); strings.Join(got, ",") != relayRotatePath+","+relayRegisterPath {
		t.Fatalf("Bloem relay saw %v, want the refused rotation, then a registration", got)
	}
	siloCredentialUnchanged(t, server, silo)
}

// A relay failure is the relay's error, for the route to map as Silo's does,
// and leaves Bloem's stored credential as it was.
func TestRegisterBloemRelayKeepsTheCredentialWhenTheRelayFails(t *testing.T) {
	server, _, bloemRelay, _ := bloemRelayFixture(t)
	server.store.values[BloemSettingPushRelayAPIKey] = ""
	server.store.values[BloemSettingPushRelayReregister] = "true"
	bloemRelay.refuse(relayRegisterPath, http.StatusTooManyRequests, "rate_limited")

	_, err := server.admin.RegisterBloemRelay(context.Background())
	var relayErr RelayCredentialError
	if !errors.As(err, &relayErr) || relayErr.Status != http.StatusTooManyRequests {
		t.Fatalf("register error = %v, want the relay's 429", err)
	}
	if server.store.values[BloemSettingPushRelayReregister] != "true" || server.store.values[BloemSettingPushRelayAPIKey] != "" {
		t.Fatal("a failed registration changed Bloem's parked credential")
	}
}

// Without Bloem's relay sender (no push backing) there is nothing to register.
func TestRegisterBloemRelayUnavailableWithoutASender(t *testing.T) {
	var none *AdminPushService
	if _, err := none.RegisterBloemRelay(context.Background()); !errors.Is(err, ErrBloemRelayUnavailable) {
		t.Fatalf("nil service register = %v, want ErrBloemRelayUnavailable", err)
	}
	if _, err := (&AdminPushService{}).RegisterBloemRelay(context.Background()); !errors.Is(err, ErrBloemRelayUnavailable) {
		t.Fatalf("senderless register = %v, want ErrBloemRelayUnavailable", err)
	}
}
