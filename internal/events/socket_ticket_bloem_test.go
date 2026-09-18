package events

import (
	"encoding/json"
	"testing"
)

func TestSocketTicketBloemBindingSerializationAndSingleUse(t *testing.T) {
	want := socketIdentity()
	want.AuthorityBinding = `{"version":1,"tenant":"original"}`
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SocketIdentity
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	store := NewSocketTicketStore(nil)
	ticket, err := store.Mint(t.Context(), decoded)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Consume(t.Context(), ticket)
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthorityBinding != want.AuthorityBinding || got.ProfileToken != want.ProfileToken || got.AccessFingerprint != want.AccessFingerprint {
		t.Fatal("stored ticket lost delegated authority")
	}
	if _, err := store.Consume(t.Context(), ticket); err == nil {
		t.Fatal("bound ticket replay accepted")
	}
}
