package api

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/listcollections"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	"github.com/Silo-Server/silo-server/internal/requests"
)

// ListCollectionRuntime retains the same native request service used by HTTP.
// Source origin is public configuration, separate from plugin credentials.
type ListCollectionRuntime struct {
	service *listcollections.Service
	managed *managedtracking.Service
	domain  *listCollectionDomain
	origin  string
}

func newListCollectionRuntime(deps Dependencies, managed *managedtracking.Service, source *managedCalendarSource) *ListCollectionRuntime {
	origin, err := url.Parse(strings.TrimSpace(deps.PastimeListOrigin))
	if err != nil || origin == nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" && origin.Path != "/" {
		return nil
	}
	domain := &listCollectionDomain{managed: managed, source: source}
	return &ListCollectionRuntime{service: &listcollections.Service{Pool: deps.DB, Cipher: deps.SecretCipher, Domain: domain}, managed: managed, domain: domain, origin: origin.Scheme + "://" + origin.Host}
}
func configureListCollectionRequests(deps Dependencies, native *requests.Service) {
	r := deps.ListCollections
	if r == nil || r.service == nil || native == nil {
		return
	}
	r.service.Requests = &listCollectionRequests{domain: r.domain, movies: native, books: BuildBloemBookRequestsService(deps)}
	r.managed.SetListBackend(&listCollectionBackend{service: r.service})
}

// StartListCollectionMaintenance is called once by worker composition. Closing
// waits for active work, so it cannot outlive the host database lifecycle.
func StartListCollectionMaintenance(ctx context.Context, deps Dependencies, enabled bool) func() {
	r := deps.ListCollections
	if !enabled || r == nil || r.service == nil || r.service.Requests == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			if err := r.service.Sweep(ctx, 100); err != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "List collection cleanup failed", "error", err)
			}
			if err := r.service.QueueDue(ctx, 30); err != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "List collection scheduling failed", "error", err)
			}
			if _, err := r.service.DeliverRequests(ctx, 10); err != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "List collection request delivery failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return func() { cancel(); wg.Wait() }
}
