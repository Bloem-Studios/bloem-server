package audiobooks

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

func TestBloemAudiobookLibraryFilterWidth(t *testing.T) {
	ctx := t.Context()
	pool := newABSIdentityDatabase(t)
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_folders(id,name,type,enabled) VALUES(7,'Runtime width fixture','audiobooks',true);
		INSERT INTO media_items(content_id,type,title) VALUES('width-runtime','audiobook','Width runtime');
		INSERT INTO media_files(media_folder_id,file_path,content_id,duration)
		VALUES(7,'/fixture/width-runtime.m4b','width-runtime',3600);`); err != nil {
		t.Fatal(err)
	}
	store := &ABSMediaStore{Pool: pool}
	const wide = 2147483648
	for _, tc := range []struct {
		name     string
		allowed  []int
		disabled []int
		library  int64
		seconds  int
	}{
		{name: "unrestricted", seconds: 3600},
		{name: "explicit-deny-all", allowed: []int{}, seconds: 0},
		{name: "allowed-with-wide-id", allowed: []int{7, wide}, seconds: 3600},
		{name: "wide-id-does-not-admit-low-id", allowed: []int{wide}, seconds: 0},
		{name: "disabled-wide-id", disabled: []int{wide}, seconds: 3600},
		{name: "disabled-low-and-wide", allowed: []int{7, wide}, disabled: []int{7, wide}, seconds: 0},
		{name: "wide-route-pin", allowed: []int{7, wide}, library: wide, seconds: 0},
		{name: "low-route-pin", allowed: []int{7, wide}, library: 7, seconds: 3600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := &models.MediaItem{ContentID: "width-runtime"}
			err := store.hydrateAudiobookRuntime(ctx, []*models.MediaItem{item}, catalog.AccessFilter{
				AllowedLibraryIDs: tc.allowed, DisabledLibraryIDs: tc.disabled,
			}, tc.library)
			if err != nil {
				t.Fatal(err)
			}
			if item.AudiobookDurationSeconds != tc.seconds {
				t.Fatalf("duration=%d, want %d", item.AudiobookDurationSeconds, tc.seconds)
			}
		})
	}
}
