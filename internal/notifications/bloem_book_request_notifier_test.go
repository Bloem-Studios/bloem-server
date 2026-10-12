package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/requests"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type bookNoticeBackendStub struct {
	admins     bool
	recipients []requests.Follower
	events     []string
	err        error
}

func (s *bookNoticeBackendStub) bookRequestRecipients(_ context.Context, _ bookrequests.Request, admins bool) ([]requests.Follower, error) {
	s.admins = admins
	return s.recipients, s.err
}
func (s *bookNoticeBackendStub) dispatchBookRequestNotice(_ context.Context, _ bookrequests.Request, _ requests.Follower, event string) error {
	s.events = append(s.events, event)
	return s.err
}

func TestBookRequestNotificationAdapterScopesEventsAndPropagatesFulfilmentFailure(t *testing.T) {
	for _, event := range []string{"submitted", "approve", "decline", "fulfilled"} {
		t.Run(event, func(t *testing.T) {
			backend := &bookNoticeBackendStub{recipients: []requests.Follower{{UserID: 1, ProfileID: "viewer"}}}
			n := &BookRequestNotifier{backend: backend}
			if err := n.notify(t.Context(), bookrequests.Request{}, event); err != nil {
				t.Fatal(err)
			}
			if backend.admins != (event == "submitted") || len(backend.events) != 1 || backend.events[0] != event {
				t.Fatalf("wrong routing: %+v", backend)
			}
		})
	}
	backend := &bookNoticeBackendStub{err: errors.New("temporary failure")}
	n := &BookRequestNotifier{backend: backend}
	if err := n.Fulfilled(t.Context(), bookrequests.Request{}); !errors.Is(err, backend.err) {
		t.Fatalf("lost retryable failure: %v", err)
	}
	n.Lifecycle(t.Context(), bookrequests.Request{}, "auto_approved")
	if len(backend.events) != 0 {
		t.Fatal("policy approval duplicated personal confirmation")
	}
	// The book adapter deliberately offers no deployment-wide announce hook.
	if _, ok := any(n).(interface {
		AnnounceFulfilled(context.Context, bookrequests.Request)
	}); ok {
		t.Fatal("tenant title could be broadcast deployment-wide")
	}
}

func TestBookRequestNoticesCurrentTenantRecipientsDedupeAndManualCopyDatabase(t *testing.T) {
	pool := inboxPageDB(t)
	org, foreign := uuid.New(), uuid.New()
	if _, err := pool.Exec(t.Context(), `
	CREATE TABLE organizations(id uuid PRIMARY KEY,status text,is_default boolean);
	CREATE TABLE users(id integer PRIMARY KEY,enabled boolean);
	CREATE TABLE user_profiles(user_id integer,id text,organization_id uuid);
	CREATE TABLE organization_memberships(account_id integer,organization_id uuid,status text,legacy_role text);
	INSERT INTO users VALUES(1,true),(2,true),(3,true),(4,true),(5,false),(6,true);
	INSERT INTO organizations VALUES($1,'active',false),($2,'active',false);
	INSERT INTO user_profiles VALUES(1,'viewer',$1),(1,'sibling',$1),(2,'admin',$1),(3,'admin',$2),(4,'inactive-admin',$1),(5,'disabled-admin',$1),(6,'account-admin-only',$1);
	INSERT INTO organization_memberships VALUES(1,$1,'active','user'),(2,$1,'active','admin'),(3,$2,'active','admin'),(4,$1,'suspended','admin'),(5,$1,'active','admin'),(6,$1,'active','user');`, pgx.QueryExecModeSimpleProtocol, org, foreign); err != nil {
		t.Fatal(err)
	}
	system := newAccessSystem(pool, nil)
	n := NewBookRequestNotifier(system)
	r := bookrequests.Request{ID: uuid.NewString(), OrganizationID: org, MediaType: "audiobook", Title: "A requested book", Status: "approved", Outcome: "active", RequestedByUserID: 1, RequestedByProfileID: "viewer", Fulfilment: "manual"} //nolint:misspell // Existing Go field preserves the book-request wire vocabulary.
	for range 2 {
		for _, event := range []string{"submitted", "approve", "decline", "fulfilled"} {
			if err := n.notify(t.Context(), r, event); err != nil {
				t.Fatal(err)
			}
		}
	}
	rows, err := pool.Query(t.Context(), `SELECT user_id,profile_id,type,body FROM notification_deliveries ORDER BY user_id,id`)
	if err != nil {
		t.Fatal(err)
	}
	type notice struct {
		User          int
		Profile, Type string
		Body          []byte
	}
	got, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (notice, error) {
		var n notice
		err := row.Scan(&n.User, &n.Profile, &n.Type, &n.Body)
		return n, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("retries not deduped or recipients leaked: %+v", got)
	}
	manual, submitted, approved, declined := false, false, false, false
	for _, row := range got {
		var body AlertBody
		if err = json.Unmarshal(row.Body, &body); err != nil {
			t.Fatal(err)
		}
		switch body.Title {
		case "New audiobook request":
			submitted = true
			if row.User != 2 || row.Profile != "admin" || body.Deeplink != "/admin/requests?kind=audiobook" || strings.Contains(body.Body, "awaiting review") {
				t.Fatalf("submitted notice leaked/misstated: %+v %+v", row, body)
			}
		case "Audiobook request fulfilled":
			manual = true
			if strings.Contains(strings.ToLower(body.Body), "available") || body.Deeplink != "/requests?kind=audiobook" {
				t.Fatalf("manual fulfillment claims availability: %+v", body)
			}
		case "Audiobook request approved":
			approved = true
		case "Audiobook request declined":
			declined = true
		default:
			t.Fatalf("unexpected notice: %+v %+v", row, body)
		}
		if row.Type != DeliveryTypeSystemAlert {
			t.Fatalf("wrong operational type: %s", row.Type)
		}
		if body.Title != "New audiobook request" && (row.User != 1 || row.Profile != "viewer" || body.Deeplink != "/requests?kind=audiobook") {
			t.Fatalf("requester notice leaked/misdirected: %+v %+v", row, body)
		}
	}
	if !manual || !submitted || !approved || !declined {
		t.Fatalf("missing notices: %+v", got)
	}
	// Stale request attribution does not authorize delivery after revocation.
	if _, err = pool.Exec(t.Context(), `UPDATE organization_memberships SET status='suspended' WHERE organization_id=$1`, org); err != nil {
		t.Fatal(err)
	}
	r.ID = uuid.NewString()
	for _, event := range []string{"submitted", "approve", "decline", "fulfilled"} {
		if err = n.notify(t.Context(), r, event); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = pool.QueryRow(t.Context(), `SELECT count(*) FROM notification_deliveries`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("revoked membership got notice: count=%d err=%v", count, err)
	}
}

func TestAudiobookAttentionUsesCurrentOrganizationAdmins(t *testing.T) {
	backend := &bookNoticeBackendStub{recipients: []requests.Follower{{UserID: 2, ProfileID: "admin"}}}
	n := &BookRequestNotifier{backend: backend}
	n.Attention(t.Context(), bookrequests.Request{MediaType: "audiobook", Title: "Synthetic Saga"}, 3)
	if !backend.admins || len(backend.events) != 1 || backend.events[0] != "attention:3" {
		t.Fatalf("attention routing %+v", backend)
	}
	alert, err := bookRequestAlert(bookrequests.Request{MediaType: "audiobook", Title: "Synthetic Saga"}, "attention:3")
	if err != nil || alert.Deeplink != "/admin/requests?kind=audiobook" || !strings.Contains(alert.Title, "needs attention") {
		t.Fatalf("alert %+v %v", alert, err)
	}
}
