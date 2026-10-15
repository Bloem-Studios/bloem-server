package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
)

func TestValidateNativeStorageCredentials(t *testing.T) {
	good := nativeStorageCredentialsCommand{ExpectedRevision: 3, AccessKey: "AK", SecretKey: "SK"}
	if err := validateNativeStorageCredentials(good); err != nil {
		t.Fatalf("valid command rejected: %v", err)
	}
	withToken := good
	withToken.SessionToken = "TOKEN"
	if err := validateNativeStorageCredentials(withToken); err != nil {
		t.Fatalf("command with session token rejected: %v", err)
	}
	for name, cmd := range map[string]nativeStorageCredentialsCommand{
		"missing revision":      {AccessKey: "AK", SecretKey: "SK"},
		"zero revision":         {ExpectedRevision: 0, AccessKey: "AK", SecretKey: "SK"},
		"negative revision":     {ExpectedRevision: -1, AccessKey: "AK", SecretKey: "SK"},
		"missing access key":    {ExpectedRevision: 1, SecretKey: "SK"},
		"missing secret":        {ExpectedRevision: 1, AccessKey: "AK"},
		"control in access key": {ExpectedRevision: 1, AccessKey: "A\x00K", SecretKey: "SK"},
		"control in secret":     {ExpectedRevision: 1, AccessKey: "AK", SecretKey: "S\nK"},
		"control in token":      {ExpectedRevision: 1, AccessKey: "AK", SecretKey: "SK", SessionToken: "T\tK"},
	} {
		if err := validateNativeStorageCredentials(cmd); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

// Unknown fields are not rejected by validateNativeStorageCredentials: the
// shared command decoder refuses any member the command struct does not name,
// so a rotation body cannot carry bucket, endpoint or other connection fields.
func TestCredentialRotationBodyRejectsUnknownFields(t *testing.T) {
	var cmd nativeStorageCredentialsCommand
	if _, err := nativeStorageDecodeJSON([]byte(`{"expected_revision":1,"access_key":"AK","secret_key":"SK"}`), &cmd); err != nil {
		t.Fatalf("valid body rejected: %v", err)
	}
	var other nativeStorageCredentialsCommand
	if _, err := nativeStorageDecodeJSON([]byte(`{"expected_revision":1,"access_key":"AK","secret_key":"SK","bucket":"other"}`), &other); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestCredentialRotationRequiresAdminContext(t *testing.T) {
	h := NewBloemNativeStorageManagementHandler(nil).ForAdminScope(auth.AdminScopePlatform)
	w := httptest.NewRecorder()
	h.HandleRotateCredentials(w, httptest.NewRequest("POST", "/sources/00000000-0000-4000-8000-000000000001/credentials", strings.NewReader(`{"expected_revision":1,"access_key":"AK","secret_key":"SK"}`)))
	if w.Code != 401 {
		t.Fatalf("anonymous credential rotation status %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "SK") {
		t.Fatal("response echoed a secret")
	}
}

func TestCredentialRotationCapabilityFollowsReadiness(t *testing.T) {
	for _, ready := range []bool{false, true} {
		h := &BloemNativeStorageManagementHandler{Capabilities: audioCapabilityFixture{}}
		if !ready {
			h.Capabilities = nil
		}
		if got := h.nativeStorageCapabilities(t.Context())["credential_rotation_in_use"]; got != ready {
			t.Fatalf("ready=%v credential_rotation_in_use=%v", ready, got)
		}
	}
}
