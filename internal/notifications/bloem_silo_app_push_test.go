package notifications

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/requests"
)

// Silo's apps on a Bloem server register in push_devices and are woken
// through Silo's relay (docs/architecture/bloem-push-relays.md). They read a
// woken delivery from Silo's notification routes, which carry no alert text,
// so a Bloem-only notice reaches a Silo phone as a notification that says
// nothing ("System", "Open Silo to view it"). A Silo server never sends one.
// Each Bloem producer still writes the inbox row; none queues a push for a
// Silo-app device. A Silo type sent the same way still does, so the fixture
// is not what keeps the attempts away.
func TestBloemOnlyNoticesQueueNoSiloAppPush(t *testing.T) {
	system, p := siloAppPushSystem(t)
	ctx := t.Context()

	// Control: a Silo notice reaches both Silo-app devices.
	control, err := system.DispatchOperational(ctx, Delivery{
		ID: "silo-request-approved", UserID: 1, ProfileID: "p1", Type: DeliveryTypeRequestApproved,
		ReasonFlags: []byte(`{"request_id":"req-1","title":"Heat"}`),
	}, OperationalDispatch{})
	if err != nil || control == nil {
		t.Fatalf("dispatch Silo notice: row=%v err=%v", control, err)
	}

	// An upgrade prompt (system.alert).
	if err := system.dispatchUpgradePrompt(ctx, 1, "p1", "bloem-upgrade-prompt", AlertBody{
		Title: "Watch on more screens", Body: "Your plan allows one stream.", Severity: SeverityInfo,
		Dismissible: true, CTA: &AlertCTA{Label: "Upgrade", URL: "https://example.com/upgrade"},
	}); err != nil {
		t.Fatalf("dispatch upgrade prompt: %v", err)
	}
	// A book request notice (system.alert).
	if err := system.dispatchBookRequestNotice(ctx,
		bookrequests.Request{ID: "book-1", Title: "Example Book", MediaType: "audiobook"},
		requests.Follower{UserID: 1, ProfileID: "p1"}, "approve"); err != nil {
		t.Fatalf("dispatch book request notice: %v", err)
	}
	// An announcement (system.announcement), through the batch path.
	body, err := json.Marshal(AlertBody{Title: "Maintenance tonight", Body: "Back by ten.", Severity: SeverityInfo, Dismissible: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := system.DispatchOperationalBatch(ctx, []Delivery{{
		ID: "bloem-announcement", UserID: 1, ProfileID: "p1", Type: DeliveryTypeSystemAnnouncement, Body: body,
	}}, OperationalDispatch{}); err != nil {
		t.Fatalf("dispatch announcement: %v", err)
	}

	pushed, inbox := pushedAndInboxByType(t, p)
	if got := strings.Join(pushed[DeliveryTypeRequestApproved], ","); got != "silo-android,silo-iphone" {
		t.Fatalf("Silo notice pushed to %q, want both Silo-app devices", got)
	}
	for _, deliveryType := range []string{DeliveryTypeSystemAlert, DeliveryTypeSystemAnnouncement} {
		if devices := pushed[deliveryType]; len(devices) > 0 {
			t.Errorf("%s queued a push for Silo-app devices %v; Silo's apps cannot show it", deliveryType, devices)
		}
	}
	if inbox[DeliveryTypeSystemAlert] != 2 || inbox[DeliveryTypeSystemAnnouncement] != 1 {
		t.Fatalf("inbox rows = %v, want 2 system.alert (upgrade prompt, book request) and 1 system.announcement", inbox)
	}
}

// siloAppPushSystem is a System with Apple and Android delivery on and one
// Silo-app device of each platform for account 1's profile p1.
func siloAppPushSystem(t *testing.T) (*System, *pgxpool.Pool) {
	t.Helper()
	p := inboxPageDB(t)
	if _, err := p.Exec(t.Context(), `
		CREATE TABLE push_devices (LIKE public.push_devices INCLUDING ALL);
		CREATE TABLE push_delivery_attempts (LIKE public.push_delivery_attempts INCLUDING ALL);
		INSERT INTO push_devices
			(id, user_id, profile_id, device_id, platform, provider, apns_environment, apns_topic,
			 apns_token_ciphertext, apns_token_hash, server_device_id, push_mode, enabled)
		VALUES
			('silo-iphone', 1, 'p1', 'local-apple', 'apple', 'silo_relay', 'production',
			 'org.siloserver.silo', 'ciphertext', 'hash-apple', 'server-apple', 'private_push', true);
		INSERT INTO push_devices
			(id, user_id, profile_id, device_id, platform, provider, fcm_token_ciphertext,
			 fcm_token_hash, server_device_id, push_mode, enabled)
		VALUES
			('silo-android', 1, 'p1', 'local-android', 'android', 'silo_relay', 'ciphertext',
			 'hash-android', 'server-android', 'private_push', true)`); err != nil {
		t.Fatalf("create push tables: %v", err)
	}
	return &System{
		pool: p,
		Settings: NewSettings(mapSettingReader{
			SettingApplePushDeliveryEnabled:   "true",
			SettingAndroidPushDeliveryEnabled: "true",
		}),
		Deliveries:     NewDeliveryRepository(p),
		Preferences:    NewPreferencesRepository(p),
		pushDeviceRepo: NewPushDeviceRepository(p),
		dispatcher:     NewMultiDispatcher(),
		logger:         slog.New(slog.DiscardHandler),
	}, p
}

// pushedAndInboxByType answers, per delivery type, the devices push attempts
// were queued for (sorted) and the number of inbox rows.
func pushedAndInboxByType(t *testing.T, p *pgxpool.Pool) (map[string][]string, map[string]int) {
	t.Helper()
	rows, err := p.Query(t.Context(), `
		SELECT d.type, a.push_device_id
		FROM push_delivery_attempts a
		JOIN notification_deliveries d ON d.id = a.notification_delivery_id
		ORDER BY d.type, a.push_device_id`)
	if err != nil {
		t.Fatalf("query push attempts: %v", err)
	}
	pushed := map[string][]string{}
	for rows.Next() {
		var deliveryType, device string
		if err := rows.Scan(&deliveryType, &device); err != nil {
			t.Fatalf("scan: %v", err)
		}
		pushed[deliveryType] = append(pushed[deliveryType], device)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read push attempts: %v", err)
	}
	counts, err := p.Query(t.Context(), `SELECT type, count(*) FROM notification_deliveries GROUP BY type`)
	if err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	inbox := map[string]int{}
	for counts.Next() {
		var deliveryType string
		var n int
		if err := counts.Scan(&deliveryType, &n); err != nil {
			t.Fatalf("scan count: %v", err)
		}
		inbox[deliveryType] = n
	}
	if err := counts.Err(); err != nil {
		t.Fatalf("read counts: %v", err)
	}
	return pushed, inbox
}

// Silo's DispatchOperational queues a push for every Silo-app device of the
// recipient. A Bloem-only delivery type goes through DispatchBloemOperational
// instead, which writes the same rows minus that push. This fails by name
// when a producer anywhere in the server hands Silo's dispatch a system.*
// delivery.
func TestBloemOnlyDeliveriesAvoidSiloDispatch(t *testing.T) {
	root := filepath.Join("..", "..")
	var offenders []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "DispatchOperational" {
				return true
			}
			for _, arg := range call.Args {
				if deliveryTypeIsBloemOnly(arg) {
					rel, _ := filepath.Rel(root, path)
					offenders = append(offenders, filepath.ToSlash(rel))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("Bloem-only deliveries sent through Silo's DispatchOperational (use DispatchBloemOperational): %v", offenders)
	}
}

// deliveryTypeIsBloemOnly reports whether expr is a Delivery literal whose
// Type is DeliveryTypeSystemAlert or DeliveryTypeSystemAnnouncement.
func deliveryTypeIsBloemOnly(expr ast.Expr) bool {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return false
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "Type" {
			continue
		}
		var name string
		switch v := kv.Value.(type) {
		case *ast.Ident:
			name = v.Name
		case *ast.SelectorExpr:
			name = v.Sel.Name
		}
		if name == "DeliveryTypeSystemAlert" || name == "DeliveryTypeSystemAnnouncement" {
			return true
		}
	}
	return false
}
