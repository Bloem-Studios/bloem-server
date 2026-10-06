//go:build integration

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/nodeconfig"
	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/Silo-Server/silo-server/internal/proxy"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
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

func TestNativeContainmentProxyCurrentFileAuthorityDB(t *testing.T) {
	pool := nativeContainmentDatabase(t)
	var account, folder, file int
	var org, owner uuid.UUID
	if err := pool.QueryRow(t.Context(), "INSERT INTO users(username,role) VALUES('i1-proxy','user') RETURNING id").Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT id FROM organizations WHERE is_default").Scan(&org); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE organizations SET status='active',owner_account_id=$1 WHERE id=$2", account, org); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) VALUES($1,$2,'active','user') ON CONFLICT(organization_id,account_id) DO UPDATE SET status='active'", org, account); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT id FROM resource_owners WHERE organization_id=$1", org).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('ebooks','I1 proxy',$1) RETURNING id", owner).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "INSERT INTO media_files(content_id,media_folder_id,file_path,file_size) VALUES('i1-proxy',$1,'ordinary.epub',7) RETURNING id", folder).Scan(&file); err != nil {
		t.Fatal(err)
	}
	authority := newProxySourceAccess(pool)
	if err := authority.AllowMediaSource(t.Context(), account, file); err != nil {
		t.Fatalf("real local authority must first allow: %v", err)
	}
	var relays atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { relays.Add(1); _, _ = w.Write([]byte("artifact-bytes")) }))
	defer origin.Close()
	watcher := nodeconfig.NewWatcher(nil, nil, nil, nodeconfig.BootstrapOverrides{})
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "i1-proxy-secret"
	watcher.SetConfigForTest(cfg)
	server := proxy.NewServer(watcher, nodesessions.NewTracker(nil, "http://proxy-i1", "proxy-i1", "proxy"))
	server.SetSourceAccess(authority)
	for _, tc := range []struct {
		name, path string
		status     int
	}{{"ordinary", "ordinary.epub", 200}, {"native", "bloem-storage:legacy-empty-token", 404}} {
		if _, err := pool.Exec(t.Context(), "UPDATE media_files SET file_path=$1 WHERE id=$2", tc.path, file); err != nil {
			t.Fatal(err)
		}
		for _, method := range []string{"GET", "HEAD"} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				claims := streamtoken.Claims{SessionID: "old-remote-artifact", PlayMethod: streamtoken.PlayMethodDownload, UserID: account, MediaFileID: file, MediaPath: "", TranscodeNode: origin.URL, DownloadArtifactID: "artifact-1"}
				token, err := streamtoken.Sign(claims, cfg.Auth.JWTSecret, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				before := relays.Load()
				rr := httptest.NewRecorder()
				server.Handler().ServeHTTP(rr, httptest.NewRequest(method, "/downloads/file/"+token, nil))
				if rr.Code != tc.status {
					t.Errorf("status=%d want=%d body=%s", rr.Code, tc.status, rr.Body.String())
				}
				if tc.status == 404 && relays.Load() != before {
					t.Error("native current file relayed old empty-path token")
				}
			})
		}
	}
	if err := authority.AllowMediaSource(t.Context(), account, file); !errors.Is(err, proxy.ErrSourceHidden) {
		t.Errorf("native authority=%v", err)
	}
	// Current missing-file contract is scanner.ErrFileNotFound, not pgx.ErrNoRows.
	if err := authority.AllowMediaSource(t.Context(), account, file+10000000); !errors.Is(err, proxy.ErrSourceHidden) {
		t.Errorf("missing file authority=%v", err)
	}
	// Existing resource policy still runs before native containment.
	if _, err := pool.Exec(t.Context(), "UPDATE organizations SET status='suspended' WHERE id=$1", org); err != nil {
		t.Fatal(err)
	}
	if err := authority.AllowMediaSource(t.Context(), account, file); !errors.Is(err, proxy.ErrSourceHidden) {
		t.Errorf("suspended authority=%v", err)
	}
}
