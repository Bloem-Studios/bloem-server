package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

func TestRetainedIntentStrictDecoding(t *testing.T) {
	intent := pgstore.FirstAdmissionIntent{InstallationID: "11111111-1111-4111-8111-111111111111", AccountID: 1, ExpectedUsername: "test-account", Backend: "postgres", SourceID: "22222222-2222-4222-8222-222222222222", IntentID: "33333333-3333-4333-8333-333333333333"}
	raw, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readIntent(strings.NewReader(string(raw)))
	if err != nil || got != intent {
		t.Fatalf("retained intent: %+v %v", got, err)
	}
	for _, bad := range []string{`{}`, string(raw) + ` {}`, strings.Replace(string(raw), `"postgres"`, `"sqlite"`, 1), strings.Replace(string(raw), `"account_id":1`, `"account_id":0`, 1), string(raw[:len(raw)-1]) + `,"apply":true}`, string(raw) + strings.Repeat(" ", 17000)} {
		if _, err := readIntent(strings.NewReader(bad)); err == nil {
			t.Fatal("invalid or ambiguous intent accepted")
		}
	}
}
