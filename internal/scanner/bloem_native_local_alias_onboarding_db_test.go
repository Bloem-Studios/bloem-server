//go:build integration

package scanner

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func localAliasOnboardingDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("SILO_TEST_DATABASE_URL") != "" {
		t.Fatal("API database environment must be unset")
	}
	path := "../../.superpowers/sdd/2026-10-06-native-storage-persistence/database-url"
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("private mode-0600 fixture required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read private fixture")
	}
	template, err := pgxpool.ParseConfig(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal("parse private template identity")
	}
	dsn, cleanup, err := bloemtestdb.CloneNativeOnboarding(t.Context(), strings.TrimSpace(string(b)), false)
	if err != nil {
		t.Fatal(err)
	}
	var p *pgxpool.Pool
	t.Cleanup(func() {
		if p != nil {
			p.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := cleanup(ctx); err != nil {
			t.Error(err)
		} else {
			t.Log("private UUID clone cleanup verified")
		}
	})
	cfg, err := bloemtestdb.NativeOnboardingPoolConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	p, err = pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("open guarded private clone")
	}
	var actual string
	if err = p.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil || actual != cfg.ConnConfig.Database || actual == template.ConnConfig.Database {
		t.Fatal("actual database is not independent owned clone")
	}
	t.Logf("owned private UUID clone: %s", actual)
	if err = bloemtestdb.PrepareNativeOnboardingPool(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if !catalog.NativeStorageSchemaReady(t.Context(), p) {
		t.Fatal("full current guard graph required")
	}
	return p
}

func localAliasNativeL1(t *testing.T, p *pgxpool.Pool) int {
	t.Helper()
	var owner uuid.UUID
	if err := p.QueryRow(t.Context(), "SELECT bloem_platform_resource_owner_id()").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	tx, err := p.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	state, err := catalog.NewFolderRepository(p).CreateNativeEbookTx(t.Context(), tx, catalog.NativeLibraryCreate{OwnerID: owner, CreationKey: uuid.New(), Name: "Unrelated native L1"})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return state.LibraryID
}

func localAliasClass(t *testing.T, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, key, want string) {
	t.Helper()
	var got string
	if err := q.QueryRow(t.Context(), "SELECT bloem_native_item_class($1)", key).Scan(&got); err != nil || got != want {
		t.Fatalf("alias classification=%s want=%s error=%v", got, want, err)
	}
}

// Exercise the actual local podcast publication methods with a parsed show.
// Parsing/ffprobe is outside this database correction; all catalog writers,
// root lookup, conflict identity preservation and episode refresh are real.
func TestNativeOnboardingLocalPodcastPublicationDB(t *testing.T) {
	p := localAliasOnboardingDB(t)
	native := localAliasNativeL1(t, p)
	var folder int
	if err := p.QueryRow(t.Context(), "INSERT INTO media_folders(type,name) VALUES('podcast','Local podcasts') RETURNING id").Scan(&folder); err != nil {
		t.Fatal(err)
	}
	s := &Scanner{fileRepo: NewFileRepository(p), itemRepo: catalog.NewItemRepository(p), episodeRepo: catalog.NewEpisodeRepository(p)}
	root := "/local-podcast/" + uuid.NewString()
	show := &parsedPodcastShow{Title: "Ordinary show", Episodes: []parsedPodcastEpisode{{Path: root + "/episode.mp3", Title: "Ordinary episode", Track: 1}}}
	item, err := s.upsertPodcastMediaItem(t.Context(), folder, root, show)
	if err != nil {
		t.Fatal("actual podcast item publication", err)
	}
	if err = s.upsertPodcastEpisodesAndFiles(t.Context(), &models.MediaFolder{ID: folder}, item, root, show); err != nil {
		t.Fatal("actual podcast episode/file publication", err)
	}
	var episode string
	var file int
	if err = p.QueryRow(t.Context(), "SELECT f.id,e.content_id FROM media_files f JOIN episodes e ON e.content_id=f.episode_id WHERE f.file_path=$1 AND f.content_id=$2 AND e.series_id=$2 AND e.season_id IS NULL", show.Episodes[0].Path, item).Scan(&file, &episode); err != nil {
		t.Fatal("podcast publication shape", err)
	}
	localAliasClass(t, p, episode, "local")
	again, err := s.upsertPodcastMediaItem(t.Context(), folder, root, show)
	if err != nil || again != item {
		t.Fatal("actual podcast root lookup changed item", err)
	}
	if err = s.upsertPodcastEpisodesAndFiles(t.Context(), &models.MediaFolder{ID: folder}, again, root, show); err != nil {
		t.Fatal("actual unchanged podcast rescan", err)
	}
	var stable bool
	if err = p.QueryRow(t.Context(), "SELECT id=$2 AND episode_id=$3 AND content_id=$4 FROM media_files WHERE file_path=$1", show.Episodes[0].Path, file, episode, item).Scan(&stable); err != nil || !stable {
		t.Fatal("rescan changed retained identities", err)
	}
	// Unchanged file and episode metadata writes remain legal at higher
	// isolation with an unrelated native L1. New admission remains RC-only.
	for _, iso := range []pgx.TxIsoLevel{pgx.ReadCommitted, pgx.RepeatableRead, pgx.Serializable} {
		tx, err := p.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: iso})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err = tx.Exec(t.Context(), "UPDATE episodes SET title=title WHERE content_id=$1", episode); err != nil {
			t.Fatal("unchanged podcast metadata", iso, err)
		}
		mf := models.MediaFile{MediaFolderID: folder, FilePath: show.Episodes[0].Path, ContentID: item, EpisodeID: episode, CanonicalRootPath: root, ObservedRootPath: root}
		actual, err := s.fileRepo.UpsertTx(t.Context(), tx, mf)
		if err != nil || actual.ID != file || actual.EpisodeID != episode {
			t.Fatal("unchanged podcast file", iso, err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	_, err = s.fileRepo.Upsert(t.Context(), models.MediaFile{MediaFolderID: native, FilePath: "native-alias-" + uuid.NewString(), ContentID: item, EpisodeID: episode})
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "BN001" {
		t.Fatalf("native endpoint must refuse BN001; class=%T", err)
	}
}

func TestNativeOnboardingSeasonlessSeriesPublicationDB(t *testing.T) {
	p := localAliasOnboardingDB(t)
	localAliasNativeL1(t, p)
	var folder int
	if err := p.QueryRow(t.Context(), "INSERT INTO media_folders(type,name) VALUES('series','Ordinary series') RETURNING id").Scan(&folder); err != nil {
		t.Fatal(err)
	}
	parent := uuid.NewString()
	if err := catalog.NewItemRepository(p).Upsert(t.Context(), &models.MediaItem{ContentID: parent, Type: "series", Title: "Ordinary series"}); err != nil {
		t.Fatal(err)
	}
	for _, withSeason := range []bool{false, true} {
		episode := &models.Episode{ContentID: uuid.NewString(), SeriesID: parent, SeasonNumber: 1, EpisodeNumber: 1, Title: "Episode"}
		if withSeason {
			season := &models.Season{ContentID: uuid.NewString(), SeriesID: parent, SeasonNumber: 2, Title: "Season two"}
			if err := catalog.NewSeasonRepository(p).Upsert(t.Context(), season); err != nil {
				t.Fatal(err)
			}
			episode.SeasonID = season.ContentID
			episode.SeasonNumber = 2
		}
		repo := catalog.NewEpisodeRepository(p)
		if err := repo.Upsert(t.Context(), episode); err != nil {
			t.Fatal(err)
		}
		localAliasClass(t, p, episode.ContentID, "local")
		mf := models.MediaFile{MediaFolderID: folder, FilePath: "series-alias-" + uuid.NewString(), ContentID: parent, EpisodeID: episode.ContentID}
		saved, err := NewFileRepository(p).Upsert(t.Context(), mf)
		if err != nil {
			t.Fatal("actual series episode/file publication", err)
		}
		if err = repo.Upsert(t.Context(), episode); err != nil {
			t.Fatal("actual series episode metadata refresh", err)
		}
		for _, iso := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
			tx, err := p.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: iso})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			actual, err := NewFileRepository(p).UpsertTx(t.Context(), tx, mf)
			if err != nil || actual.ID != saved.ID {
				t.Fatal("unchanged series alias at higher isolation", iso, err)
			}
			if _, err = tx.Exec(t.Context(), "UPDATE episodes SET title=title WHERE content_id=$1", episode.ContentID); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestNativeOnboardingContradictoryEpisodeAliasesDB(t *testing.T) {
	p := localAliasOnboardingDB(t)
	localAliasNativeL1(t, p)
	for _, category := range []string{"missing-parent", "wrong-parent-type", "wrong-season-parent", "missing-season", "colliding-parent-role", "colliding-season-role", "self-parent", "self-season", "season-equals-parent"} {
		t.Run(category, func(t *testing.T) {
			tx, err := p.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := tx.Exec(t.Context(), q, args...); err != nil {
					t.Fatal(err)
				}
			}
			parent, episode, season, other := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
			var folder int
			if err = tx.QueryRow(t.Context(), "INSERT INTO media_folders(type,name) VALUES('series','Contradictory alias') RETURNING id").Scan(&folder); err != nil {
				t.Fatal(err)
			}
			exec("INSERT INTO media_items(content_id,type,title) VALUES($1,'series','Parent'),($2,'series','Other')", parent, other)
			// Isolated retained-corruption controls only. Restore all trigger enabled
			// states before testing final classifier and actual FileRepository.
			exec("ALTER TABLE episodes DISABLE TRIGGER ALL")
			exec("ALTER TABLE seasons DISABLE TRIGGER ALL")
			epParent, epSeason := parent, season
			seasonParent := parent
			switch category {
			case "missing-parent":
				epParent = uuid.NewString()
			case "wrong-parent-type":
				exec("UPDATE media_items SET type='movie' WHERE content_id=$1", parent)
			case "wrong-season-parent":
				seasonParent = other
			case "missing-season":
				epSeason = uuid.NewString()
			case "colliding-parent-role":
				exec("INSERT INTO seasons(content_id,series_id,season_number) VALUES($1,$2,2)", parent, other)
			case "colliding-season-role":
				exec("INSERT INTO media_items(content_id,type,title) VALUES($1,'series','Colliding season')", season)
			case "self-parent":
				epParent = episode
			case "self-season":
				epSeason = episode
			case "season-equals-parent":
				epSeason = parent
			}
			exec("INSERT INTO seasons(content_id,series_id,season_number) VALUES($1,$2,1)", season, seasonParent)
			exec("INSERT INTO episodes(content_id,series_id,season_id,season_number,episode_number,title) VALUES($1,$2,$3,1,1,'Contradictory')", episode, epParent, epSeason)
			exec("ALTER TABLE episodes ENABLE TRIGGER ALL")
			exec("ALTER TABLE seasons ENABLE TRIGGER ALL")
			if !catalog.NativeStorageSchemaReady(t.Context(), tx) {
				t.Fatal("negative fixture lacks exact final graph")
			}
			localAliasClass(t, tx, episode, "inconsistent")
			exec("SAVEPOINT refused_alias")
			_, err = NewFileRepository(p).UpsertTx(t.Context(), tx, models.MediaFile{MediaFolderID: folder, FilePath: "contradictory-" + uuid.NewString(), ContentID: parent, EpisodeID: episode})
			var pe *pgconn.PgError
			if !errors.As(err, &pe) || pe.Code != "BN001" {
				t.Fatalf("contradictory endpoint must refuse BN001; class=%T", err)
			}
			exec("ROLLBACK TO SAVEPOINT refused_alias")
			var count int
			if err = tx.QueryRow(t.Context(), "SELECT count(*) FROM media_files WHERE media_folder_id=$1", folder).Scan(&count); err != nil || count != 0 {
				t.Fatal("refused alias published a file", err)
			}
		})
	}
}
