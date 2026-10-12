//go:build integration

package pluginhost_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"google.golang.org/protobuf/encoding/protojson"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/Silo-Server/silo-server/internal/cache"
	"github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/pluginhost"
	"github.com/Silo-Server/silo-server/internal/plugins"
)

// fakePlugin captures the host-side state we need per started plugin to wire
// the dispatcher.
type fakePlugin struct {
	installationID int
	pluginID       string
	binaryPath     string
	manifest       *pluginv1.PluginManifest
	client         *pluginhost.Client
}

// dispatchTestStore satisfies the dispatcher's internal taskInstallationStore
// interface by name structurally — both methods reference only exported types
// (plugins.Installation, plugins.Capability).
type dispatchTestStore struct {
	installations []*plugins.Installation
	capabilities  map[int][]*plugins.Capability
}

func (s *dispatchTestStore) ListEnabled(_ context.Context) ([]*plugins.Installation, error) {
	return s.installations, nil
}

func (s *dispatchTestStore) ListCapabilities(_ context.Context, id int) ([]*plugins.Capability, error) {
	return s.capabilities[id], nil
}

// dispatchTestResolver maps installation IDs back to their host-side
// pluginhost.Client so the dispatcher can dispense an EventConsumer.
type dispatchTestResolver struct {
	clients map[int]*pluginhost.Client
}

func (r *dispatchTestResolver) EventConsumerClient(_ context.Context, installationID int, capabilityID string) (*pluginhost.EventConsumerClient, error) {
	c, ok := r.clients[installationID]
	if !ok {
		return nil, fmt.Errorf("no client for installation %d", installationID)
	}
	return c.EventConsumer(capabilityID)
}

// inMemoryBus is a minimal cache.EventBus for tests. The dispatcher subscribes
// to it but for this integration test we drive events through the hub directly
// (plugin->host RuntimeHost.PublishEvent feeds the hub), so the bus never sees
// traffic. A working impl is still required for dispatcher.Start.
type inMemoryBus struct {
	mu       sync.Mutex
	handlers map[string][]cache.EventHandler
}

func newInMemoryBus() *inMemoryBus { return &inMemoryBus{handlers: map[string][]cache.EventHandler{}} }

func (b *inMemoryBus) Publish(_ context.Context, channel string, ev cache.Event) error {
	b.mu.Lock()
	hs := append([]cache.EventHandler(nil), b.handlers[channel]...)
	b.mu.Unlock()
	for _, h := range hs {
		h(ev)
	}
	return nil
}

func (b *inMemoryBus) Subscribe(_ context.Context, channel string, h cache.EventHandler) error {
	b.mu.Lock()
	b.handlers[channel] = append(b.handlers[channel], h)
	b.mu.Unlock()
	return nil
}

func (b *inMemoryBus) Close() error { return nil }

// capabilitiesFromManifest mirrors the production conversion: it asks
// plugins.CapabilityRecordsFromManifest for the host-side records (which
// flatten the proto's Subscriptions/DisplayName/etc into Metadata["..."]),
// then stamps InstallationID on each.
func capabilitiesFromManifest(t *testing.T, installationID int, manifest *pluginv1.PluginManifest) []*plugins.Capability {
	t.Helper()
	records, err := plugins.CapabilityRecordsFromManifest(manifest)
	if err != nil {
		t.Fatalf("CapabilityRecordsFromManifest: %v", err)
	}
	out := make([]*plugins.Capability, 0, len(records))
	for i := range records {
		records[i].InstallationID = installationID
		out = append(out, &records[i])
	}
	return out
}

var dispatchFixtureBuild = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "silo-dispatch-fixture-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, "dispatch")
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/fakedispatch")
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build dispatch fixture: %w\n%s", err, output)
	}
	return os.ReadFile(bin)
})

func buildDispatchFixture(t *testing.T, name string) string {
	t.Helper()
	binary, err := dispatchFixtureBuild()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(out, binary, 0o755); err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(binary)
	manifest := &pluginv1.PluginManifest{
		PluginId: "example." + name, Version: "1.0.0", SiloApiVersion: "v1",
		Checksum:           hex.EncodeToString(checksum[:]),
		SupportedPlatforms: []*pluginv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}},
		Capabilities:       []*pluginv1.CapabilityDescriptor{{Type: "scheduled_task.v1", Id: "emit-once"}, {Type: "scheduled_task.v1", Id: "emit-targeted"}, {Type: "scheduled_task.v1", Id: "ping"}},
	}
	if name == "fake-consumer" || name == "fake-target" {
		manifest.Capabilities = append(manifest.Capabilities, &pluginv1.CapabilityDescriptor{Type: "event_consumer.v1", Id: "events", Subscriptions: []string{"plugin.example.fake-router.submitted", "plugin.example.fake-publisher.foo-event"}})
	}
	data, err := protojson.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(out), "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return out
}

// startFakePlugin builds a repository-local SDK fixture, reads the proto
// manifest through its CLI, and starts the real broker-backed plugin process.
func startFakePlugin(t *testing.T, host *pluginhost.Host, name string, installationID int) *fakePlugin {
	t.Helper()
	bin := buildDispatchFixture(t, name)

	manifestBytes, err := exec.Command(bin, "manifest").Output()
	if err != nil {
		t.Fatalf("get manifest from %s: %v", name, err)
	}
	manifest := &pluginv1.PluginManifest{}
	if err := protojson.Unmarshal(manifestBytes, manifest); err != nil {
		t.Fatalf("parse manifest protojson for %s: %v", name, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := host.Start(ctx, pluginhost.StartRequest{
		InstallationID: installationID,
		BinaryPath:     bin,
		Manifest:       manifest,
	})
	if err != nil {
		t.Fatalf("host.Start(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = host.Stop(installationID) })

	return &fakePlugin{
		installationID: installationID,
		pluginID:       manifest.GetPluginId(),
		binaryPath:     bin,
		manifest:       manifest,
		client:         client,
	}
}

// triggerScheduledTask fires the given scheduled-task capability on the plugin.
func triggerScheduledTask(t *testing.T, p *fakePlugin, capabilityID string) {
	t.Helper()
	taskClient, err := p.client.ScheduledTask(capabilityID)
	if err != nil {
		t.Fatalf("ScheduledTask(%s) on %s: %v", capabilityID, p.pluginID, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := taskClient.Run(ctx, &pluginv1.RunScheduledTaskRequest{TaskKey: capabilityID}); err != nil {
		t.Fatalf("Run(%s) on %s: %v", capabilityID, p.pluginID, err)
	}
}

// hubObserver subscribes to a hub and records every envelope it sees.
type hubObserver struct {
	mu     sync.Mutex
	events []events.Envelope
}

func newHubObserver(t *testing.T, hub *events.Hub) *hubObserver {
	t.Helper()
	o := &hubObserver{}
	ch, unsub := hub.Subscribe()
	t.Cleanup(unsub)
	go func() {
		for env := range ch {
			o.mu.Lock()
			o.events = append(o.events, env)
			o.mu.Unlock()
		}
	}()
	return o
}

func (o *hubObserver) seen() []events.Envelope {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]events.Envelope, len(o.events))
	copy(out, o.events)
	return out
}

func (o *hubObserver) count(name string) int {
	count := 0
	for _, env := range o.seen() {
		if env.Event == name {
			count++
		}
	}
	return count
}

func (o *hubObserver) waitFor(t *testing.T, predicate func(events.Envelope) bool, timeout time.Duration) (events.Envelope, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, env := range o.seen() {
			if predicate(env) {
				return env, true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return events.Envelope{}, false
}

// TestEventNameDispatchEndToEnd verifies manifest event-name subscriptions
// loaded from the installation store across the real plugin broker.
func TestEventNameDispatchEndToEnd(t *testing.T) {
	bus := newInMemoryBus()
	hub := events.NewHub("test", bus)
	if err := hub.Start(context.Background()); err != nil {
		t.Fatalf("hub.Start: %v", err)
	}

	observer := newHubObserver(t, hub)

	host := pluginhost.NewHost(pluginhost.Config{
		Logger:         hclog.NewNullLogger(),
		EventPublisher: hub,
	})

	router := startFakePlugin(t, host, "fake-router", 1)
	consumer := startFakePlugin(t, host, "fake-consumer", 2)
	peer := startFakePlugin(t, host, "fake-target", 3)

	store := &dispatchTestStore{
		installations: []*plugins.Installation{
			{ID: router.installationID, PluginID: router.pluginID, Enabled: true},
			{ID: consumer.installationID, PluginID: consumer.pluginID, Enabled: true},
			{ID: peer.installationID, PluginID: peer.pluginID, Enabled: true},
		},
		capabilities: map[int][]*plugins.Capability{
			router.installationID:   capabilitiesFromManifest(t, router.installationID, router.manifest),
			consumer.installationID: capabilitiesFromManifest(t, consumer.installationID, consumer.manifest),
			peer.installationID:     capabilitiesFromManifest(t, peer.installationID, peer.manifest),
		},
	}
	resolver := &dispatchTestResolver{clients: map[int]*pluginhost.Client{
		router.installationID:   router.client,
		consumer.installationID: consumer.client,
		peer.installationID:     peer.client,
	}}

	dispatcher := plugins.NewEventDispatcherWithTypedResolver(bus, hub, store, resolver, 4)
	if err := dispatcher.Start(context.Background()); err != nil {
		t.Fatalf("dispatcher.Start: %v", err)
	}
	t.Cleanup(dispatcher.Stop)

	triggerScheduledTask(t, router, "emit-once")

	emitted := "plugin." + router.pluginID + ".submitted"
	forwarded := "plugin." + consumer.pluginID + ".received." + emitted

	if _, ok := observer.waitFor(t, func(env events.Envelope) bool { return env.Event == emitted }, 5*time.Second); !ok {
		t.Fatalf("router emission %q never reached the hub. seen: %v", emitted, eventNames(observer.seen()))
	}
	if _, ok := observer.waitFor(t, func(env events.Envelope) bool { return env.Event == forwarded }, 5*time.Second); !ok {
		t.Fatalf("consumer never re-published %q (event-name dispatch failed). seen: %v", forwarded, eventNames(observer.seen()))
	}
	peerForward := "plugin." + peer.pluginID + ".received." + emitted
	if _, ok := observer.waitFor(t, func(env events.Envelope) bool { return env.Event == peerForward }, 5*time.Second); !ok {
		t.Fatalf("second subscriber never received %q. seen: %v", peerForward, eventNames(observer.seen()))
	}

	dispatcher.Stop()
	triggerScheduledTask(t, router, "emit-once")
	if _, ok := observer.waitFor(t, func(env events.Envelope) bool { return env.Event == emitted && observer.count(emitted) == 2 }, 5*time.Second); !ok {
		t.Fatal("publisher stopped emitting after dispatcher shutdown")
	}
	if _, ok := observer.waitFor(t, func(events.Envelope) bool { return observer.count(forwarded) > 1 || observer.count(peerForward) > 1 }, 750*time.Millisecond); ok {
		t.Fatal("stopped dispatcher delivered another event")
	}
	if err := host.Stop(router.installationID); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Client(router.installationID); !errors.Is(err, pluginhost.ErrClientNotFound) {
		t.Fatalf("stopped publisher remains available: %v", err)
	}
}

// TestTargetedPublishReachesOnlyNamedPlugin asserts that when a publisher uses
// PublishEventTo, only the named target plugin's HandleEvent is invoked even
// though another consumer subscribes to the same event by name.
func TestTargetedPublishReachesOnlyNamedPlugin(t *testing.T) {
	bus := newInMemoryBus()
	hub := events.NewHub("test", bus)
	if err := hub.Start(context.Background()); err != nil {
		t.Fatalf("hub.Start: %v", err)
	}

	observer := newHubObserver(t, hub)

	host := pluginhost.NewHost(pluginhost.Config{
		Logger:         hclog.NewNullLogger(),
		EventPublisher: hub,
	})

	publisher := startFakePlugin(t, host, "fake-publisher", 10)
	consumer := startFakePlugin(t, host, "fake-consumer", 11) // also subscribes to foo-event by name; should NOT receive
	target := startFakePlugin(t, host, "fake-target", 12)     // subscribes to foo-event by name; SHOULD receive

	store := &dispatchTestStore{
		installations: []*plugins.Installation{
			{ID: publisher.installationID, PluginID: publisher.pluginID, Enabled: true},
			{ID: consumer.installationID, PluginID: consumer.pluginID, Enabled: true},
			{ID: target.installationID, PluginID: target.pluginID, Enabled: true},
		},
		capabilities: map[int][]*plugins.Capability{
			publisher.installationID: capabilitiesFromManifest(t, publisher.installationID, publisher.manifest),
			consumer.installationID:  capabilitiesFromManifest(t, consumer.installationID, consumer.manifest),
			target.installationID:    capabilitiesFromManifest(t, target.installationID, target.manifest),
		},
	}
	resolver := &dispatchTestResolver{clients: map[int]*pluginhost.Client{
		publisher.installationID: publisher.client,
		consumer.installationID:  consumer.client,
		target.installationID:    target.client,
	}}

	dispatcher := plugins.NewEventDispatcherWithTypedResolver(bus, hub, store, resolver, 4)
	if err := dispatcher.Start(context.Background()); err != nil {
		t.Fatalf("dispatcher.Start: %v", err)
	}
	t.Cleanup(dispatcher.Stop)

	triggerScheduledTask(t, publisher, "emit-targeted")

	emitted := "plugin." + publisher.pluginID + ".foo-event"
	targetForward := "plugin." + target.pluginID + ".received." + emitted
	consumerForward := "plugin." + consumer.pluginID + ".received." + emitted

	if _, ok := observer.waitFor(t, func(env events.Envelope) bool { return env.Event == emitted }, 5*time.Second); !ok {
		t.Fatalf("publisher emission %q never reached the hub. seen: %v", emitted, eventNames(observer.seen()))
	}
	if _, ok := observer.waitFor(t, func(env events.Envelope) bool { return env.Event == targetForward }, 5*time.Second); !ok {
		t.Fatalf("target never re-published %q (targeted dispatch failed). seen: %v", targetForward, eventNames(observer.seen()))
	}

	// Now confirm the non-target consumer remained silent. Wait a bit past the
	// point where target already forwarded — if consumer were going to receive,
	// it would have done so by now.
	if _, ok := observer.waitFor(t, func(env events.Envelope) bool { return env.Event == consumerForward }, 750*time.Millisecond); ok {
		t.Fatalf("non-target consumer received %q — target filter failed. seen: %v", consumerForward, eventNames(observer.seen()))
	}
}

func eventNames(envs []events.Envelope) string {
	names := make([]string, 0, len(envs))
	for _, e := range envs {
		names = append(names, e.Event)
	}
	return strings.Join(names, ", ")
}
