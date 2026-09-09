package organizations_test

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/organizations"
)

func TestMediaAccessUsesCurrentAccountAndLibrary(t *testing.T) {
	pool := ownershipDatabase(t)
	_, err := pool.Exec(t.Context(), `
 CREATE TABLE organizations(id bigint PRIMARY KEY,status text);
 CREATE TABLE users(id integer PRIMARY KEY,organization_id bigint,enabled boolean);
 CREATE TABLE media_folders(id integer PRIMARY KEY,organization_id bigint);
 CREATE TABLE media_files(id integer PRIMARY KEY,media_folder_id integer);
 CREATE TABLE organization_library_grants(organization_id bigint,media_folder_id integer);
 INSERT INTO organizations VALUES(1,'active'),(2,'active');
 INSERT INTO users VALUES(10,1,true),(20,2,true);
 INSERT INTO media_folders VALUES(11,1),(22,2),(33,NULL),(44,NULL);
 INSERT INTO media_files SELECT id, id FROM media_folders;
 INSERT INTO organization_library_grants VALUES(1,33),(2,33);
 `)
	if err != nil {
		t.Fatal(err)
	}
	repo := organizations.NewRepository(pool)
	check := func(user, file int, want bool) {
		t.Helper()
		got, err := repo.CanAccessMediaFile(t.Context(), user, file)
		if err != nil || got != want {
			t.Errorf("user %d file %d: %t %v want %t", user, file, got, err, want)
		}
	}
	check(10, 11, true)
	check(10, 22, false)
	check(10, 33, true)
	check(20, 33, true)
	check(10, 44, false)
	check(10, 999, false)
	check(999, 11, false)
	if _, err = pool.Exec(t.Context(), `DELETE FROM organization_library_grants WHERE organization_id=1`); err != nil {
		t.Fatal(err)
	}
	check(10, 33, false)
	check(20, 33, true)
	if _, err = pool.Exec(t.Context(), `UPDATE organizations SET status='suspended' WHERE id=2; UPDATE users SET enabled=false WHERE id=10`); err != nil {
		t.Fatal(err)
	}
	check(20, 33, false)
	check(20, 22, false)
	check(10, 11, false)
}
