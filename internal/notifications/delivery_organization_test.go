package notifications

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Delivery rows represent already-enqueued work. Revoke authority after
// insertion and exercise the exact reads used by push/webhook and digest sends.
func TestQueuedDeliveryReadsRecheckOrganizationAccess(t *testing.T) {
	pool := inboxPageDB(t)
	repo := NewDeliveryRepository(pool)
	profile := uuid.NewString()
	_, err := pool.Exec(t.Context(), `
 CREATE TABLE organizations(id bigint PRIMARY KEY,status text);
 CREATE TABLE users(id integer PRIMARY KEY,organization_id bigint);
 CREATE TABLE media_folders(id integer PRIMARY KEY,organization_id bigint);
 CREATE TABLE organization_library_grants(organization_id bigint,media_folder_id integer);
 INSERT INTO organizations VALUES(1,'active'),(2,'active');
 INSERT INTO users VALUES(10,1);
 INSERT INTO media_folders VALUES(11,1),(22,2),(33,NULL),(44,NULL);
 INSERT INTO organization_library_grants VALUES(1,33);
 `)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for name, library := range map[string]*int{"private": new(11), "foreign": new(22), "shared": new(33), "ungranted": new(44), "account": nil} {
		id := uuid.NewString()
		ids[name] = id
		_, err = pool.Exec(t.Context(), `INSERT INTO notification_deliveries(id,user_id,profile_id,library_id,type,reason_flags,status) VALUES($1,10,$2,$3,'request.approved','{}','pending')`, id, profile, library)
		if err != nil {
			t.Fatal(err)
		}
	}
	since := Cursor{CreatedAt: time.Unix(0, 0), ID: ""}
	check := func(names ...string) {
		t.Helper()
		want := []string{}
		for _, name := range names {
			want = append(want, ids[name])
		}
		slices.Sort(want)
		for name, id := range ids {
			row, err := repo.GetRowByID(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if (row != nil) != slices.Contains(names, name) {
				t.Errorf("queued %s: present=%t", name, row != nil)
			}
		}
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		for _, byUser := range []bool{false, true} {
			var rows []DeliveryRow
			if byUser {
				rows, err = repo.ListForUserSince(t.Context(), tx, 10, since, time.Time{}, 50)
			} else {
				rows, err = repo.ListForProfileSince(t.Context(), tx, profile, since, time.Time{}, 50)
			}
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, row := range rows {
				got = append(got, row.ID)
			}
			slices.Sort(got)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("digest byUser=%t: got %v want %v", byUser, got, want)
			}
		}
		for _, read := range []func() (bool, error){
			func() (bool, error) { return repo.HasForUserSince(t.Context(), 10, since) },
			func() (bool, error) { return repo.HasForProfileSince(t.Context(), profile, since) },
			func() (bool, error) { return repo.HasTransactionalForUserSince(t.Context(), 10, since) },
			func() (bool, error) { return repo.HasTransactionalForProfileSince(t.Context(), profile, since) },
		} {
			pending, err := read()
			if err != nil {
				t.Fatal(err)
			}
			if pending != (len(want) > 0) {
				t.Errorf("pending=%t want=%t", pending, len(want) > 0)
			}
		}
	}
	check("private", "shared", "account")
	if _, err = pool.Exec(t.Context(), `DELETE FROM organization_library_grants WHERE organization_id=1`); err != nil {
		t.Fatal(err)
	}
	check("private", "account")
	if _, err = pool.Exec(t.Context(), `UPDATE organizations SET status='suspended' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	check()
	// With an active organization but only inaccessible media notices left,
	// the sweep must not keep finding work that its send query will discard.
	if _, err = pool.Exec(t.Context(), `UPDATE organizations SET status='active' WHERE id=1;
 DELETE FROM notification_deliveries WHERE library_id IS NULL OR library_id=11`); err != nil {
		t.Fatal(err)
	}
	check()
}
