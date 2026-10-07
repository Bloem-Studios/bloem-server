package handlers

import (
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

func TestNativePhaseServiceError(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{{"unauthenticated", 401}, {"forbidden", 403}, {"not_found", 404}, {"native_local_operation_unsupported", 409}, {"native_storage_unavailable", 503}} {
		refused := &catalog.NativePhaseRefusal{Code: tc.code}
		mapped := nativePhaseServiceError(refused, 500, "internal_error", "old error")
		if mapped.Status != tc.status || !catalog.IsNativePhaseRefusal(mapped) {
			t.Fatalf("%s lost: %+v", tc.code, mapped)
		}
	}
	ordinary := nativePhaseServiceError(errors.New("ordinary"), 400, "bad_request", "original message")
	if ordinary.Status != 400 || ordinary.Code != "bad_request" || ordinary.Message != "original message" || ordinary.Unwrap() != nil {
		t.Fatal("ordinary error changed")
	}
}
