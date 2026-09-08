package apiv2

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

func TestPlaybackOrdinaryAbortUsesExistingTerminalSchema(t *testing.T) {
	// Recovery returns the core decision directly, so validate its actual JSON,
	// including omitted session/plan fields, against the existing public schema.
	response := playback.NewTerminalResponseV3("playback_start_aborted", "Playback could not start. Start playback again explicitly.", false)
	raw, err := json.Marshal(PlaybackStartResult{value: response})
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err = json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	r := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	schema := huma.SchemaFromType(r, reflect.TypeFor[PlaybackDecision]())
	result := new(huma.ValidateResult)
	huma.Validate(r, schema, huma.NewPathBuffer([]byte("body"), 0), huma.ModeReadFromServer, value, result)
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	object := value.(map[string]any)
	for _, field := range []string{"recovery", "session_id", "playback_plan", "accepted", "stop_id"} {
		if _, ok := object[field]; ok {
			t.Fatalf("unexpected %s", field)
		}
	}
}

// Optional owned-Postgres integration regression: the existing fixture creates
// only its own identities and uses the real admission/control/source seams.
// It is skipped when no explicitly supplied test database is available.
func TestPlaybackOrdinaryAbortHTTPRecovery(t *testing.T) {
	f := newRecoveryHTTPFixture(t, "postgres", "pending")
	ctx := t.Context()
	abortID := uuid.NewString()
	if _, err := f.store.CancelInitialActivation(ctx, f.binding, abortID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sink.StopPlaybackProgress(ctx, userstore.StopPlaybackProgressRequest{Scope: f.binding.Scope, Fence: f.binding.Fence, StopID: abortID}); err != nil {
		t.Fatal(err)
	}
	observed, err := playback.ReadInitialActivationReceiptV3(ctx, f.binding, f.sink)
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.store.CompleteInitialAbort(ctx, f.binding, abortID, observed)
	if err != nil {
		t.Fatal(err)
	}
	if first.Terminal == nil || first.Terminal.Last != nil {
		t.Fatal("missing no-progress terminal")
	}
	var original string
	for i := range 2 {
		f.restart(t)
		response := f.call(t, "POST", "/start", f.body)
		if response.Code != http.StatusCreated {
			t.Fatalf("boot%d: %d %s", i, response.Code, response.Body.String())
		}
		var decision playback.DecisionResponseV3
		if err := json.Unmarshal(response.Body.Bytes(), &decision); err != nil {
			t.Fatal(err)
		}
		if decision.Terminal == nil || decision.Terminal.Reason != "playback_start_aborted" || decision.SessionID != "" || decision.PlaybackPlan != nil {
			t.Fatal("missing ordinary terminal")
		}
		if i == 0 {
			original = response.Body.String()
		} else if original != response.Body.String() {
			t.Fatal("changed exact replay")
		}
	}
	retained, err := f.store.ReadInitialActivation(ctx, f.binding)
	if err != nil || !reflect.DeepEqual(first, retained) {
		t.Fatal("changed durable identity/receipt", err)
	}
	changed := make(map[string]any, len(f.body))
	for k, v := range f.body {
		changed[k] = v
	}
	changed["file_id"] = "999999"
	if response := f.call(t, "POST", "/start", changed); response.Code != http.StatusConflict {
		t.Fatalf("changed body: %d %s", response.Code, response.Body.String())
	}
}
