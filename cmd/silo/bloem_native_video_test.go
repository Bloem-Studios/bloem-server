package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/nodeconfig"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
	"github.com/redis/go-redis/v9"
)

// Workers without shared grants or reviewed artifacts retain local operation
// and create no plugin installation directories.
func TestStandaloneNativeVideoWithoutConfigurationKeepsLocalNode(t *testing.T) {
	for _, missing := range []string{"redis", "approvals", "empty-approvals"} {
		t.Run(missing, func(t *testing.T) {
			watcher := nodeconfig.NewWatcher(nil, nil, nil, nodeconfig.BootstrapOverrides{})
			watcher.SetConfigForTest(&config.Config{})
			server := transcodenode.NewServer(watcher, nil)
			root := filepath.Join(t.TempDir(), "native-storage")
			deps := api.Dependencies{}
			approvals := ""
			if missing == "redis" {
				approvals = filepath.Join(t.TempDir(), "unused-approvals.json")
			}
			if missing != "redis" {
				deps.RedisClient = redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
				defer deps.RedisClient.Close()
			}
			if missing == "empty-approvals" {
				protected, err := os.MkdirTemp("/tmp", "bloem-worker-approvals-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(protected) })
				approvals = filepath.Join(protected, "approvals.json")
				if err := os.WriteFile(approvals, []byte("{}"), 0400); err != nil {
					t.Fatal(err)
				}
			}
			handler, shutdown, err := configureStandaloneNativeVideo(server, deps, 7, root, approvals)
			if err != nil || handler == nil || shutdown == nil {
				t.Fatalf("ordinary worker held: %v", err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("health unavailable: %d", response.Code)
			}
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unconfigured worker created native host artifacts")
			}
			if err := shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Executor shutdown must join its input owners before the storage manager
// considers closing admission. If joining fails, its host remains alive.
func TestStandaloneNativeVideoShutdownJoinsBeforeHost(t *testing.T) {
	var order []string
	shutdown := nativeVideoShutdown(func(context.Context) error { order = append(order, "executor"); return nil }, func(context.Context) error { order = append(order, "host"); return nil })
	if err := shutdown(t.Context()); err != nil || len(order) != 2 || order[0] != "executor" || order[1] != "host" {
		t.Fatalf("wrong join order: %v %v", order, err)
	}
	order = nil
	sentinel := errors.New("executor still owned")
	shutdown = nativeVideoShutdown(func(context.Context) error { order = append(order, "executor"); return sentinel }, func(context.Context) error { order = append(order, "host"); return nil })
	if err := shutdown(t.Context()); !errors.Is(err, sentinel) || len(order) != 1 {
		t.Fatalf("host closed before executor joined: %v %v", order, err)
	}
}
