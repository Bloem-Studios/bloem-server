package audiobooks

import (
	"errors"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"

	"github.com/Silo-Server/silo-server/internal/audiobooks/abs"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/scanner"
)

func TestABSHidesNativeAudioButKeepsLocalTracksDB(t *testing.T) {
	pool := newABSIdentityDatabase(t)
	if _, err := pool.Exec(t.Context(), `INSERT INTO media_folders(id,name,type,enabled) VALUES(7,'Fixture','audiobooks',true);
 INSERT INTO media_items(content_id,type,title) VALUES('mixed','audiobook','Mixed'),('native','audiobook','Native');
 INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES('mixed',7),('native',7);
 INSERT INTO media_files(id,content_id,media_folder_id,file_path,duration,presentation_group_key,file_size) VALUES
 (90001,'mixed',7,'/fixture/local.m4b',50,'local',0),
 (90002,'mixed',7,'bloem-storage:mixed',100,'bloem-storage:audiobook:mixed',0),
 (90003,'native',7,'bloem-storage:native',100,'bloem-storage:audiobook:native',0);`); err != nil {
		t.Fatal(err)
	}
	store := &ABSMediaStore{Pool: pool, Items: catalog.NewItemRepository(pool), Files: scanner.NewFileRepository(pool)}
	access := catalog.AccessFilter{}
	files, err := store.GetMediaFiles(t.Context(), "mixed", access)
	if err != nil || len(files) != 1 || files[0].ID != 90001 {
		t.Fatalf("files=%v err=%v", files, err)
	}
	if _, err := store.GetMediaFileByID(t.Context(), 90002); !errors.Is(err, scanner.ErrFileNotFound) {
		t.Fatalf("native file=%v", err)
	}
	local, err := store.GetAudiobookByID(t.Context(), "mixed", access)
	if err != nil || local == nil || local.AudiobookDurationSeconds != 50 {
		t.Fatalf("local=%+v err=%v", local, err)
	}
	hidden, err := store.GetAudiobookByID(t.Context(), "native", access)
	if err != nil || hidden != nil {
		t.Fatalf("native=%+v err=%v", hidden, err)
	}
	batch, err := store.GetAudiobooksByIDs(t.Context(), []string{"mixed", "native"}, access)
	if err != nil || len(batch) != 1 || batch["mixed"] == nil {
		t.Fatalf("batch=%v err=%v", batch, err)
	}
	listed, total, err := store.ListAudiobooks(t.Context(), 7, 20, 0, access, abs.Filter{})
	if err != nil || total != 1 || len(listed) != 1 || listed[0].ContentID != "mixed" {
		t.Fatalf("list=%v total=%d err=%v", listed, total, err)
	}
	search, err := store.SearchAudiobooks(t.Context(), 7, "Native", 20, access)
	if err != nil || len(search) != 0 {
		t.Fatalf("search=%v err=%v", search, err)
	}
	var uid int
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role,enabled) VALUES('abs-audio','abs-audio@example.test','x','admin',true) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	tenants := tenancy.NewStore(pool)
	if _, err := tenants.ProvisionDefaultMembership(t.Context(), uid, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.ActivateInitialOwnership(t.Context(), uid); err != nil {
		t.Fatal(err)
	}
	user, err := pgstore.NewPostgresProvider(pool).ForUser(t.Context(), uid)
	if err != nil {
		t.Fatal(err)
	}
	profile := uuid.NewString()
	if err := user.CreateProfile(t.Context(), userstore.Profile{ID: profile, Name: "ABS listener"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"mixed", "native"} {
		if err := user.SetProgress(t.Context(), profile, id, 10, 100, userstore.ProgressThresholds{}); err != nil {
			t.Fatal(err)
		}
	}
	continuing, err := store.ListContinueListening(t.Context(), strconv.Itoa(uid), profile, 7, 20, access)
	if err != nil || len(continuing) != 1 || continuing[0].ContentID != "mixed" {
		t.Fatalf("continue=%+v err=%v", continuing, err)
	}

}

func TestABSLocalCompatibilityRequiresAccessibleLibraryDB(t *testing.T) {
	pool := newABSIdentityDatabase(t)
	if _, err := pool.Exec(t.Context(), `INSERT INTO media_folders(id,name,type,enabled) VALUES(7,'Native','audiobooks',true),(8,'Local','audiobooks',true);
 INSERT INTO media_items(content_id,type,title) VALUES('shared','audiobook','Shared');
 INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES('shared',7),('shared',8);
 INSERT INTO media_files(content_id,media_folder_id,file_path,file_size) VALUES('shared',7,'bloem-storage:shared',0),('shared',8,'/fixture/local.m4b',0)`); err != nil {
		t.Fatal(err)
	}
	store := &ABSMediaStore{Pool: pool, Items: catalog.NewItemRepository(pool), Files: scanner.NewFileRepository(pool)}
	for _, libraries := range [][]int{{7}, {7, 8}} {
		access := catalog.AccessFilter{AllowedLibraryIDs: libraries}
		want := len(libraries) == 2
		detail, err := store.GetAudiobookByID(t.Context(), "shared", access)
		if err != nil || (detail != nil) != want {
			t.Errorf("detail libraries=%v item=%v err=%v", libraries, detail, err)
		}
		batch, err := store.GetAudiobooksByIDs(t.Context(), []string{"shared"}, access)
		if err != nil || (len(batch) == 1) != want {
			t.Errorf("batch libraries=%v items=%v err=%v", libraries, batch, err)
		}
		items, total, err := store.ListAudiobooks(t.Context(), 7, 20, 0, access, abs.Filter{})
		if err != nil || (len(items) == 1) != want || (total == 1) != want {
			t.Errorf("list libraries=%v items=%v total=%d err=%v", libraries, items, total, err)
		}
	}
}
