//go:build integration

package api

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/downloads"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/streamtelemetry"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// This uses the actual router/login/membership/download service composition
// with NativeStorage nil: retained identities must stay contained after restart.
func TestNativeContainmentRouterDisabledStorageDB(t *testing.T) {
	pool := acceptanceDatabase(t)
	t.Chdir(t.TempDir())
	const key = "bloem-storage:retained-disabled"
	const body = "unrelated-router-local-bytes"
	if err := os.WriteFile(key, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	password, err := bcrypt.GenerateFromPassword([]byte("i1-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	var account, folder int
	var org, owner uuid.UUID
	if err := pool.QueryRow(t.Context(), "INSERT INTO users(username,email,password_hash,role) VALUES('i1-router','i1@example.test',$1,'user') RETURNING id", string(password)).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT id FROM organizations WHERE is_default").Scan(&org); err != nil {
		t.Fatal(err)
	}
	acceptanceSQL(t, pool, "UPDATE organizations SET status='active',owner_account_id=$1 WHERE id=$2", account, org)
	acceptanceSQL(t, pool, "INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role,download_allowed) VALUES($1,$2,'active','user',true) ON CONFLICT(organization_id,account_id) DO UPDATE SET status='active',download_allowed=true", org, account)
	if err := pool.QueryRow(t.Context(), "SELECT id FROM resource_owners WHERE organization_id=$1", org).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('ebooks','I1 router',$1) RETURNING id", owner).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	acceptanceSQL(t, pool, "INSERT INTO user_profiles(id,user_id,name,organization_id,access_group_id) SELECT 'i1-router',$1,'I1',$2,id FROM access_groups WHERE organization_id=$2 AND is_default", account, org)
	ids := map[string]int{}
	for _, tc := range []struct{ name, path string }{{"native", key}, {"ordinary", "./" + key}} {
		content := "i1-" + tc.name
		acceptanceSQL(t, pool, "INSERT INTO media_items(content_id,type,title) VALUES($1,'ebook','I1 synthetic')", content)
		acceptanceSQL(t, pool, "INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)", content, folder)
		var id int
		if err := pool.QueryRow(t.Context(), "INSERT INTO media_files(content_id,media_folder_id,file_path,file_size,container) VALUES($1,$2,$3,$4,'epub') RETURNING id", content, folder, tc.path, len(body)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[tc.name] = id
	}
	system := policy.NewSystem(policy.NewPolicyStore(pool), nil, nil)
	if err := system.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer system.Stop()
	cfg, err := config.LoadFromDB(map[string]string{"download.enabled": "true"})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Auth.JWTSecret = "i1-router-secret"
	cfg.Auth.AccessTokenExpiry = time.Hour
	cfg.Auth.RefreshTokenExpiry = 24 * time.Hour
	telemetryCfg := streamtelemetry.DefaultConfig("i1-api")
	telemetryCfg.Enabled = true
	telemetry := streamtelemetry.NewRegistry(telemetryCfg, streamtelemetry.NewLocalStore(), nil)
	t.Cleanup(func() { _ = telemetry.Stop(t.Context()) })
	router := NewRouter(Dependencies{StreamTelemetry: telemetry, NodePlanner: nodepool.NewPlanner(nodepool.NewProxyPool(), nodepool.NewTranscodePool()), DB: pool, Config: cfg, AppContext: t.Context(), FileRepo: scanner.NewFileRepository(pool), FolderRepo: catalog.NewFolderRepository(pool), UserStoreProvider: pgstore.NewPostgresProvider(pool), PolicySystem: system})
	login := performJSONRequest(t, router, "POST", "/api/v1/auth/login", `{"username":"i1-router","password":"i1-password"}`, "", nil)
	if login.Code != 200 {
		t.Fatalf("login=%d %s", login.Code, login.Body.String())
	}
	token := decodeLogin(t, login).AccessToken
	for _, version := range []string{"v1", "v2"} {
		for _, route := range []string{"direct-download", "direct-download-proxy"} {
			for _, tc := range []struct {
				name   string
				status int
			}{{"native", 404}, {"ordinary", 200}} {
				for _, method := range []string{"GET", "HEAD"} {
					t.Run(version+"/"+route+"/"+tc.name+"/"+method, func(t *testing.T) {
						path := fmt.Sprintf("/api/%s/%s?file_id=%d", version, route, ids[tc.name])
						before := len(telemetry.Snapshot().Transfers)
						rr := performJSONRequest(t, router, method, path, "", token, map[string]string{"X-Profile-Id": "i1-router"})
						if rr.Code != tc.status {
							t.Errorf("status=%d want=%d body=%s", rr.Code, tc.status, rr.Body.String())
						}
						if tc.name == "ordinary" && method == "GET" && rr.Body.String() != body {
							t.Errorf("local bytes=%q", rr.Body.String())
						}
						if tc.name == "native" && (rr.Body.String() == body || rr.Header().Get("Location") != "" || rr.Header().Get("Content-Disposition") != "") {
							t.Error("native delivery/signed redirect escaped")
						}
						if tc.name == "native" && len(telemetry.Snapshot().Transfers) != before {
							t.Error("native transfer attached")
						}
					})
				}
			}
		}
	}

	repo := downloads.NewRepository(pool)
	for _, version := range []string{"v1", "v2"} {
		for _, route := range []string{"file", "file-proxy"} {
			for _, managed := range []bool{false, true} {
				for _, tc := range []struct {
					name   string
					status int
				}{{"native", 404}, {"ordinary", 200}} {
					for _, method := range []string{"GET", "HEAD"} {
						t.Run(fmt.Sprintf("legacy/%s/%s/%v/%s/%s", version, route, managed, tc.name, method), func(t *testing.T) {
							id := uuid.NewString()
							now := time.Now()
							dl := &downloads.Download{ID: id, UserID: account, MediaFileID: ids[tc.name], ContentID: "i1-" + tc.name, Kind: downloads.KindQueued, Status: downloads.StatusReady, Format: downloads.FormatOriginal, Quality: downloads.QualityOriginal, EffectiveQuality: downloads.QualityOriginal, Revision: 1, FileSize: int64(len(body)), CreatedAt: now, UpdatedAt: now}
							headers := map[string]string{"X-Profile-Id": "i1-router"}
							if managed {
								dl.ProfileID = "i1-router"
								dl.DeviceID = id
								headers["X-Silo-Device-Id"] = id
								if err := repo.EnsureDevice(t.Context(), account, dl.ProfileID, id, "I1", "test"); err != nil {
									t.Fatal(err)
								}
							}
							if err := repo.Create(t.Context(), dl); err != nil {
								t.Fatal(err)
							}
							before := len(telemetry.Snapshot().Transfers)
							rr := performJSONRequest(t, router, method, fmt.Sprintf("/api/%s/downloads/%s/%s", version, id, route), "", token, headers)
							if rr.Code != tc.status {
								t.Errorf("legacy=%d want=%d body=%s", rr.Code, tc.status, rr.Body.String())
							}
							if tc.name == "ordinary" && method == "GET" && rr.Body.String() != body {
								t.Error("legacy local bytes differ")
							}
							if tc.name == "native" && (rr.Header().Get("Location") != "" || rr.Header().Get("Content-Disposition") != "" || rr.Body.String() == body || len(telemetry.Snapshot().Transfers) != before) {
								t.Error("legacy native bytes/target/transfer escaped")
							}
						})
					}
				}
			}
		}
	}

	for _, managed := range []bool{false, true} {
		headers := map[string]string{"X-Profile-Id": "i1-router"}
		if managed {
			headers["X-Silo-Device-Id"] = "i1-device"
		}
		payload := fmt.Sprintf(`{"content_id":"i1-native","file_id":%d,"quality":"original"}`, ids["native"])
		rr := performJSONRequest(t, router, http.MethodPost, "/api/v1/downloads", payload, token, headers)
		if rr.Code != 404 {
			t.Errorf("router create managed=%v: %d %s", managed, rr.Code, rr.Body.String())
		}
	}
}
