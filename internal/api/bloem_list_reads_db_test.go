//go:build integration

package api

import (
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/listcollections"
)

func TestListFollowReadsKeepOwnerScopeAndDetachStatus(t *testing.T) {
	s, subject, _ := listPublicationFixture(t)
	preview, err := s.QueuePreview(t.Context(), subject, listcollections.PreviewInput{URL: "https://pastime.example/lists/1"})
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.GetPreview(t.Context(), subject, preview.ID)
	if err != nil || status.Status != "pending" {
		t.Fatal(status, err)
	}
	foreign := subject
	foreign.ProfileID = "another"
	if _, err = s.GetPreview(t.Context(), foreign, preview.ID); !errors.Is(err, listcollections.ErrAuthority) {
		t.Fatal("foreign preview read", err)
	}
	if _, err = s.Pool.Exec(t.Context(), `UPDATE bloem_list_jobs SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1`, preview.ID); err != nil {
		t.Fatal(err)
	}
	status, err = s.GetPreview(t.Context(), subject, preview.ID)
	if err != nil || status.Status != "expired" {
		t.Fatal(status, err)
	}
	id := seedListDeliveryBinding(t, s, subject, listcollections.Entry{Kind: "movie", Title: "Synthetic", ExternalIDs: map[string]string{"tmdb": "42"}})
	b, counts, err := s.GetBinding(t.Context(), subject, id)
	if err != nil || b.ID != id || counts.Pending != 1 {
		t.Fatal(b, counts, err)
	}
	if _, _, err = s.GetBinding(t.Context(), foreign, id); !errors.Is(err, listcollections.ErrAuthority) {
		t.Fatal("foreign binding read", err)
	}
	if err = s.Disconnect(t.Context(), subject, id); err != nil {
		t.Fatal(err)
	}
	b, counts, err = s.GetBinding(t.Context(), subject, id)
	if err != nil || b.Status != "detached" || counts.Pending != 0 || counts.Cancelled != 1 {
		t.Fatal(b, counts, err)
	}
}
func TestListFollowRuntimeResolvesCurrentAuthorizedConnectionsAndOrigin(t *testing.T) {
	d, subject, _ := listNativeDomainFixture(t)
	deps := Dependencies{DB: d.source.pool, SecretCipher: d.managed.Cipher, BloemDependencies: BloemDependencies{PastimeListOrigin: "https://pastime.example"}}
	r := newListCollectionRuntime(deps, d.managed, d.source)
	if r == nil {
		t.Fatal("valid composition refused")
	}
	connections, err := r.Connections(t.Context(), subject.UserID, subject.ProfileID)
	if err != nil || len(connections) != 1 || connections[0].InstallationID != subject.InstallationID {
		t.Fatal(connections, err)
	}
	for _, link := range []string{"https://foreign.example/lists/1", "https://name:pass@pastime.example/lists/1", "https://pastime.example/lists/1#token"} {
		if err = r.ValidateURL(link); err == nil {
			t.Fatal("foreign URL admitted", link)
		}
	}
	if err = r.ValidateURL("https://pastime.example/lists/1"); err != nil {
		t.Fatal(err)
	}
	preview, err := r.service.QueuePreview(t.Context(), subject, listcollections.PreviewInput{URL: "https://pastime.example/lists/1"})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := r.Subject(t.Context(), subject.UserID, subject.ProfileID, 0, preview.ID, "preview")
	if err != nil || actual != subject {
		t.Fatal(actual, err)
	}
	if _, err = r.Subject(t.Context(), subject.UserID, "other", 0, preview.ID, "preview"); !errors.Is(err, listcollections.ErrAuthority) {
		t.Fatal("foreign HTTP owner", err)
	}
	h := newBloemListCollections(Dependencies{})
	if h.Service != nil || h.Backend != nil {
		t.Fatal("unconfigured capability advertised")
	}
	if _, err = d.source.pool.Exec(t.Context(), `UPDATE plugin_capabilities SET metadata='{}'::jsonb WHERE plugin_installation_id=$1`, subject.InstallationID); err != nil {
		t.Fatal(err)
	}
	connections, err = r.Connections(t.Context(), subject.UserID, subject.ProfileID)
	if err != nil || len(connections) != 0 {
		t.Fatal("old plugin falsely advertised", connections, err)
	}
	if _, err = r.service.QueuePreview(t.Context(), subject, listcollections.PreviewInput{URL: "https://pastime.example/lists/1"}); !errors.Is(err, listcollections.ErrAuthority) {
		t.Fatal("old plugin queued unsupported work", err)
	}
	if _, err = d.source.pool.Exec(t.Context(), `UPDATE plugin_installations SET enabled=false WHERE id=$1`, subject.InstallationID); err != nil {
		t.Fatal(err)
	}
	connections, err = r.Connections(t.Context(), subject.UserID, subject.ProfileID)
	if err != nil || len(connections) != 0 {
		t.Fatal("disabled connection advertised", connections, err)
	}
}
