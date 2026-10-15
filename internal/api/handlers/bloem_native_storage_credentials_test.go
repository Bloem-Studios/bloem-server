package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
)

func TestCredentialRotationBodyValidation(t *testing.T) {
	good := `{"expected_revision":3,"access_key":"AK","secret_key":"SK"}`
	if _, err := nativeStorageCredentialsDecode([]byte(good)); err != nil {
		t.Fatalf("valid body rejected: %v", err)
	}
	for name, body := range map[string]string{
		"missing revision": `{"access_key":"AK","secret_key":"SK"}`,
		"zero revision":    `{"expected_revision":0,"access_key":"AK","secret_key":"SK"}`,
		"missing secret":   `{"expected_revision":1,"access_key":"AK"}`,
		"unknown field":    `{"expected_revision":1,"access_key":"AK","secret_key":"SK","bucket":"other"}`,
		"control char":     "{\"expected_revision\":1,\"access_key\":\"A\\u0000K\",\"secret_key\":\"SK\"}",
	} {
		if _, err := nativeStorageCredentialsDecode([]byte(body)); err == nil {
			t.Fatalf("%s accepted", name)
		}
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
