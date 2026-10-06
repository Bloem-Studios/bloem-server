//go:build integration

package downloads

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func nativeContainmentDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	path := "../../.superpowers/sdd/2026-10-06-native-storage-persistence/database-url"
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("owned mode-0600 clone config required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read owned clone config")
	}
	cfg, err := pgxpool.ParseConfig(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal("invalid clone config")
	}
	template := cfg.ConnConfig.Database
	if !strings.HasPrefix(template, "bloem_storage_test_") {
		t.Fatal("refusing non-owned template")
	}
	adminCfg := cfg.Copy()
	adminCfg.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(t.Context(), adminCfg)
	if err != nil {
		t.Fatal("connect clone admin")
	}
	name := "bloem_storage_test_i1_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{template}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal("create isolated clone")
	}
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 4
	cfg.ConnConfig.RuntimeParams["bloem.membership_policy_writer"] = "v1"
	cfg.ConnConfig.RuntimeParams["bloem.schema_capability_writer"] = "v1"
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("open isolated clone")
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error("drop isolated clone")
		}
		admin.Close()
	})
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestNativeContainmentDownloadRowsDB(t *testing.T) {
	pool := nativeContainmentDatabase(t)
	// Clone configuration is resolved before changing cwd.
	t.Chdir(t.TempDir())
	const key = "bloem-storage:legacy-ready"
	const bytes = "unrelated-legacy-local-bytes"
	if err := os.WriteFile(key, []byte(bytes), 0600); err != nil {
		t.Fatal(err)
	}
	var folder, account int
	if err := pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name) VALUES('ebooks','I1') RETURNING id").Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "INSERT INTO users(username,role) VALUES('i1-download','user') RETURNING id").Scan(&account); err != nil {
		t.Fatal(err)
	}
	seedDefaultOrgMembership(t, t.Context(), pool, account)
	if _, err := pool.Exec(t.Context(), "INSERT INTO user_profiles(id,user_id,name,organization_id,access_group_id) SELECT 'i1-profile',$1,'I1',o.id,ag.id FROM organizations o JOIN access_groups ag ON ag.organization_id=o.id AND ag.is_default WHERE o.is_default", account); err != nil {
		t.Fatal(err)
	}
	var native, local int
	for _, row := range []struct {
		path, content string
		id            *int
	}{{key, "native-book", &native}, {"./" + key, "local-book", &local}} {
		if err := pool.QueryRow(t.Context(), "INSERT INTO media_files(content_id,media_folder_id,file_path,file_size,container) VALUES($1,$2,$3,$4,'epub') RETURNING id", row.content, folder, row.path, len(bytes)).Scan(row.id); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewRepository(pool)
	files := GuardNativeStorageFiles(scanner.NewFileRepository(pool))
	svc := NewService(repo, nil, NewQuantityLimiter(repo, 0, 0, 0), files, nil, nil, fakeUserRepo{&models.User{ID: account, DownloadAllowed: new(true)}}, allowDownloadItemAccess{}, nil, &config.DownloadConfig{Enabled: true})
	ctx := downloadResolvedTenantContextForAccount(account)
	for _, mode := range []string{"managed", "ephemeral"} {
		t.Run("create/"+mode, func(t *testing.T) {
			req := CreateRequest{ContentID: "native-book", FileID: native, Quality: QualityOriginal}
			if mode == "managed" {
				req.ProfileID = "i1-profile"
				req.DeviceID = "i1-device"
			}
			before := 0
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM downloads").Scan(&before); err != nil {
				t.Fatal(err)
			}
			row, err := svc.Create(ctx, account, req, catalog.AccessFilter{})
			if row != nil || !errors.Is(err, catalog.ErrItemNotFound) {
				t.Errorf("native registered row=%+v err=%v", row, err)
			}
			after := 0
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM downloads").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Error("native creation persisted a row")
			}
			req.FileID = 0
			row, err = svc.Create(ctx, account, req, catalog.AccessFilter{})
			if row != nil || !errors.Is(err, catalog.ErrItemNotFound) {
				t.Errorf("native content candidate registered row=%+v err=%v", row, err)
			}
			if _, err := pool.Exec(ctx, "DELETE FROM downloads WHERE media_file_id=$1", native); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, mode := range []string{"managed", "ephemeral"} {
		for _, tc := range []struct {
			name string
			file int
			deny bool
		}{{"native", native, true}, {"ordinary", local, false}} {
			t.Run("legacy/"+mode+"/"+tc.name, func(t *testing.T) {
				now := time.Now()
				dl := &Download{ID: fmt.Sprintf("i1-%s-%s", mode, tc.name), UserID: account, MediaFileID: tc.file, ContentID: tc.name + "-book", Kind: KindQueued, Status: StatusReady, Format: FormatOriginal, Quality: QualityOriginal, EffectiveQuality: QualityOriginal, Revision: 1, FileSize: int64(len(bytes)), CreatedAt: now, UpdatedAt: now}
				if mode == "managed" {
					dl.ProfileID = "i1-profile"
					dl.DeviceID = "i1-device"
					if err := repo.EnsureDevice(ctx, account, dl.ProfileID, dl.DeviceID, "I1", "test"); err != nil {
						t.Fatal(err)
					}
				}
				if err := repo.Create(ctx, dl); err != nil {
					t.Fatal(err)
				}
				if mode == "managed" {
					target, err := svc.ResolveManagedFile(ctx, account, dl.ProfileID, dl.DeviceID, dl.ID, catalog.AccessFilter{})
					if tc.deny && (target != nil || !errors.Is(err, catalog.ErrItemNotFound)) {
						t.Errorf("proxy target escaped: %+v %v", target, err)
					}
					// Wrong device must remain hidden before byte resolution.
					if _, err := svc.ResolveManagedFile(ctx, account, dl.ProfileID, "wrong-device", dl.ID, catalog.AccessFilter{}); !errors.Is(err, ErrNotFound) {
						t.Errorf("device auth=%v", err)
					}
				}
				rr := httptest.NewRecorder()
				err := svc.ServeFile(ctx, rr, httptest.NewRequest("GET", "/", nil), account, dl.ProfileID, dl.DeviceID, dl.ID, catalog.AccessFilter{})
				if tc.deny {
					if !errors.Is(err, catalog.ErrItemNotFound) || rr.Body.Len() != 0 {
						t.Errorf("legacy bytes=%q err=%v", rr.Body.String(), err)
					}
				} else if err != nil || rr.Body.String() != bytes {
					t.Errorf("control bytes=%q err=%v", rr.Body.String(), err)
				}
			})
		}
	}
	// Local original creation retains ready/queued semantics.
	for _, device := range []string{"", "local-device"} {
		req := CreateRequest{ContentID: "local-book", FileID: local, Quality: QualityOriginal}
		want := StatusQueued
		if device != "" {
			req.ProfileID = "i1-profile"
			req.DeviceID = device
			want = StatusReady
		}
		row, err := svc.Create(ctx, account, req, catalog.AccessFilter{})
		if err != nil || row == nil || row.Status != want {
			t.Errorf("local create=%+v err=%v", row, err)
		}
	}
	// Explicit ID never substitutes the ordinary candidate.
	raw := &nativeContainmentFiles{single: &models.MediaFile{ID: native, ContentID: "native-book", FilePath: key}, rows: []*models.MediaFile{{ID: local, ContentID: "local-book", FilePath: "./" + key}}}
	svc.fileRepo = GuardNativeStorageFiles(raw)
	if row, err := svc.Create(ctx, account, CreateRequest{ContentID: "local-book", FileID: native}, catalog.AccessFilter{}); row != nil || !errors.Is(err, catalog.ErrItemNotFound) {
		t.Errorf("explicit fallback=%+v %v", row, err)
	}
	// Actual paged bulk registration must omit a native-only episode.
	pager := &createEpisodePager{rows: []*models.Episode{{ContentID: "ep", SeasonNumber: 1, EpisodeNumber: 1}}}
	svc.episodeRepo = pager
	svc.itemRepo = createItemResolver{}
	raw.bulk = map[string][]*models.MediaFile{"ep": {{ID: native, ContentID: "series", EpisodeID: "ep", FilePath: key}}}
	page, err := svc.CreateSeriesPage(ctx, account, CreateRequest{ContentID: "series", ProfileID: "i1-profile", DeviceID: "bulk-device", BatchID: "i1-bulk"}, nil, nil, 1, catalog.AccessFilter{})
	if err != nil || len(page.Items) != 0 || len(page.Skipped) != 1 {
		t.Errorf("bulk native=%+v err=%v", page, err)
	}
}
