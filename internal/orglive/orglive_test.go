package orglive

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func healthyBus() *Bus {
	bus := NewBus()
	bus.SetHealthy(true)
	return bus
}

func change(channel Channel, id string, kind Kind) Change {
	return Change{Channel: channel, ID: id, Rev: "r-" + string(kind), Kind: kind}
}

func subscribed(channels ...Channel) *Mailbox {
	m := NewMailbox(0)
	for _, channel := range channels {
		m.Subscribe(channel)
	}
	m.Drain() // the resync every subscribe queues
	select {  // and the wake it left; a spurious wake is harmless, a lost one is not
	case <-m.Wake():
	default:
	}
	return m
}

func TestMailboxSubscribeQueuesResync(t *testing.T) {
	m := NewMailbox(0)
	m.Subscribe(ChannelPeople)
	m.Subscribe(ChannelStreams)
	frames := m.Drain()
	// Channel order, not subscription order.
	if len(frames) != 2 || !frames[0].Resync || frames[0].Channel != ChannelStreams || !frames[1].Resync || frames[1].Channel != ChannelPeople {
		t.Fatalf("frames = %+v", frames)
	}
	if got := m.Drain(); len(got) != 0 {
		t.Fatalf("a drained mailbox returned %+v", got)
	}
}

func TestMailboxIgnoresChannelsNotSubscribed(t *testing.T) {
	m := subscribed(ChannelPeople)
	m.Push(change(ChannelStreams, "s1", KindCreated))
	m.Push(change(ChannelActivity, "lifecycle:1", KindCreated))
	if frames := m.Drain(); len(frames) != 0 {
		t.Fatalf("unsubscribed channels delivered %+v", frames)
	}
	m.Unsubscribe(ChannelPeople)
	m.Push(change(ChannelPeople, "1", KindUpdated))
	if frames := m.Drain(); len(frames) != 0 {
		t.Fatalf("an unsubscribed channel delivered %+v", frames)
	}
}

func TestMailboxCoalescesPerItem(t *testing.T) {
	cases := []struct {
		name  string
		kinds []Kind
		want  []Kind // nil: cancelled out
	}{
		{"created then updated stays created", []Kind{KindCreated, KindUpdated}, []Kind{KindCreated}},
		{"created then deleted cancels out", []Kind{KindCreated, KindDeleted}, nil},
		{"updated then updated is one update", []Kind{KindUpdated, KindUpdated, KindUpdated}, []Kind{KindUpdated}},
		{"updated then deleted is deleted", []Kind{KindUpdated, KindDeleted}, []Kind{KindDeleted}},
		{"deleted then created is an update", []Kind{KindDeleted, KindCreated}, []Kind{KindUpdated}},
		{"deleted twice is deleted", []Kind{KindDeleted, KindDeleted}, []Kind{KindDeleted}},
		{"created, deleted, created is created", []Kind{KindCreated, KindDeleted, KindCreated}, []Kind{KindCreated}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := subscribed(ChannelPeople)
			for i, kind := range tc.kinds {
				c := change(ChannelPeople, "42", kind)
				c.Rev = strconv.Itoa(i)
				m.Push(c)
			}
			frames := m.Drain()
			if len(frames) != len(tc.want) {
				t.Fatalf("frames = %+v, want kinds %v", frames, tc.want)
			}
			for i, kind := range tc.want {
				if frames[i].Change.Kind != kind {
					t.Fatalf("frame %d kind = %s, want %s", i, frames[i].Change.Kind, kind)
				}
				if frames[i].Change.Rev != strconv.Itoa(len(tc.kinds)-1) {
					t.Fatalf("frame %d rev = %s, want the latest", i, frames[i].Change.Rev)
				}
			}
		})
	}
}

func TestMailboxKeepsFirstSeenOrder(t *testing.T) {
	m := subscribed(ChannelStreams)
	m.Push(change(ChannelStreams, "b", KindCreated))
	m.Push(change(ChannelStreams, "a", KindCreated))
	m.Push(change(ChannelStreams, "b", KindUpdated))
	frames := m.Drain()
	if len(frames) != 2 || frames[0].Change.ID != "b" || frames[1].Change.ID != "a" {
		t.Fatalf("frames = %+v", frames)
	}
}

func TestMailboxBurstBecomesOneResync(t *testing.T) {
	m := NewMailbox(5)
	m.Subscribe(ChannelPeople)
	m.Drain()
	for i := 0; i < 6; i++ {
		m.Push(change(ChannelPeople, strconv.Itoa(i), KindUpdated))
	}
	frames := m.Drain()
	if len(frames) != 1 || !frames[0].Resync || frames[0].Channel != ChannelPeople {
		t.Fatalf("a burst past the cap sent %+v, want one resync", frames)
	}
	// Exactly at the cap is still forwarded item by item.
	for i := 0; i < 5; i++ {
		m.Push(change(ChannelPeople, strconv.Itoa(i), KindUpdated))
	}
	if frames := m.Drain(); len(frames) != 5 {
		t.Fatalf("at the cap sent %d frames, want 5", len(frames))
	}
}

func TestMailboxResyncSwallowsPendingChanges(t *testing.T) {
	m := subscribed(ChannelPeople, ChannelStreams)
	m.Push(change(ChannelPeople, "1", KindUpdated))
	m.Push(change(ChannelStreams, "s", KindCreated))
	m.Resync(ChannelPeople)
	m.Push(change(ChannelPeople, "2", KindUpdated)) // covered by the re-read
	frames := m.Drain()
	if len(frames) != 2 || !frames[0].Resync || frames[0].Channel != ChannelPeople || frames[1].Change.ID != "s" {
		t.Fatalf("frames = %+v", frames)
	}
	m.ResyncAll()
	frames = m.Drain()
	if len(frames) != 2 || !frames[0].Resync || !frames[1].Resync {
		t.Fatalf("ResyncAll sent %+v, want a resync for each subscribed channel", frames)
	}
}

func TestMailboxWakesOnWork(t *testing.T) {
	m := subscribed(ChannelActivity)
	select {
	case <-m.Wake():
		t.Fatal("an idle mailbox woke")
	default:
	}
	m.Push(change(ChannelActivity, "lifecycle:1", KindCreated))
	select {
	case <-m.Wake():
	default:
		t.Fatal("a push did not wake the mailbox")
	}
}

func TestMailboxCloseKeepsFirstReason(t *testing.T) {
	m := NewMailbox(0)
	m.Close(ReasonFeedDown)
	m.Close(ReasonShutdown)
	select {
	case <-m.Done():
	default:
		t.Fatal("Done is open after Close")
	}
	if m.Reason() != ReasonFeedDown {
		t.Fatalf("reason = %v", m.Reason())
	}
}

// The isolation guarantee: a signal for one organization reaches the
// connections of that organization and no other.
func TestBusDeliversOnlyToTheOrganizationOfAChange(t *testing.T) {
	bus := healthyBus()
	orgA, orgB := uuid.New(), uuid.New()
	a1, a2, b1 := subscribed(ChannelPeople, ChannelStreams), subscribed(ChannelPeople), subscribed(ChannelPeople, ChannelStreams, ChannelActivity)
	for _, reg := range []struct {
		org     uuid.UUID
		account int
		m       *Mailbox
	}{{orgA, 1, a1}, {orgA, 2, a2}, {orgB, 3, b1}} {
		if _, err := bus.Register(reg.org, reg.account, reg.m); err != nil {
			t.Fatal(err)
		}
	}

	bus.Publish(orgA, change(ChannelPeople, "7", KindUpdated))
	bus.Publish(orgA, change(ChannelStreams, "s-a", KindCreated))
	bus.Publish(orgB, change(ChannelActivity, "lifecycle:9", KindCreated))

	ids := func(m *Mailbox) []string {
		var out []string
		for _, f := range m.Drain() {
			out = append(out, string(f.Channel)+"/"+f.Change.ID)
		}
		return out
	}
	if got := strings.Join(ids(a1), ","); got != "org.streams/s-a,org.people/7" && got != "org.people/7,org.streams/s-a" {
		t.Fatalf("org A connection got %q", got)
	}
	if got := strings.Join(ids(a2), ","); got != "org.people/7" {
		t.Fatalf("org A connection subscribed to people got %q", got)
	}
	if got := strings.Join(ids(b1), ","); got != "org.activity/lifecycle:9" {
		t.Fatalf("org B connection got %q: it must not see org A's changes", got)
	}
}

func TestBusDropsAChangeWithNoOrganization(t *testing.T) {
	bus := healthyBus()
	m := subscribed(ChannelPeople)
	if _, err := bus.Register(uuid.New(), 1, m); err != nil {
		t.Fatal(err)
	}
	bus.Publish(uuid.Nil, change(ChannelPeople, "1", KindUpdated))
	bus.ResyncOrganization(uuid.Nil, ChannelPeople)
	if frames := m.Drain(); len(frames) != 0 {
		t.Fatalf("a change without an organization reached %+v", frames)
	}
	if _, err := bus.Register(uuid.Nil, 1, NewMailbox(0)); !errors.Is(err, ErrNoOrganization) {
		t.Fatalf("Register without an organization = %v", err)
	}
}

func TestBusResyncOrganizationReachesOnlyThatOrganization(t *testing.T) {
	bus := healthyBus()
	orgA, orgB := uuid.New(), uuid.New()
	a, b := subscribed(ChannelStreams), subscribed(ChannelStreams)
	_, _ = bus.Register(orgA, 1, a)
	_, _ = bus.Register(orgB, 2, b)
	bus.ResyncOrganization(orgA, ChannelStreams)
	if frames := a.Drain(); len(frames) != 1 || !frames[0].Resync {
		t.Fatalf("org A got %+v", frames)
	}
	if frames := b.Drain(); len(frames) != 0 {
		t.Fatalf("org B got %+v", frames)
	}
}

func TestBusLimitsConnectionsPerAccountAndOrganization(t *testing.T) {
	bus := healthyBus()
	org := uuid.New()
	var detach []func()
	for i := 0; i < MaxConnectionsPerAccount; i++ {
		d, err := bus.Register(org, 1, NewMailbox(0))
		if err != nil {
			t.Fatalf("connection %d: %v", i, err)
		}
		detach = append(detach, d)
	}
	if _, err := bus.Register(org, 1, NewMailbox(0)); !errors.Is(err, ErrTooManyConnections) {
		t.Fatalf("sixth connection = %v, want ErrTooManyConnections", err)
	}
	// Another account in the same organization, and the same account in
	// another organization, are separate budgets.
	if _, err := bus.Register(org, 2, NewMailbox(0)); err != nil {
		t.Fatalf("another account: %v", err)
	}
	if _, err := bus.Register(uuid.New(), 1, NewMailbox(0)); err != nil {
		t.Fatalf("another organization: %v", err)
	}
	detach[0]()
	detach[0]() // safe twice
	if _, err := bus.Register(org, 1, NewMailbox(0)); err != nil {
		t.Fatalf("after a detach: %v", err)
	}
}

func TestBusFeedDownAndShutdownCloseConnections(t *testing.T) {
	bus := NewBus()
	if bus.Healthy() {
		t.Fatal("a new bus is healthy before any feed attaches")
	}
	if _, err := bus.Register(uuid.New(), 1, NewMailbox(0)); !errors.Is(err, ErrFeedDown) {
		t.Fatalf("Register while the feed is down = %v, want ErrFeedDown", err)
	}
	bus.SetHealthy(true)
	down, stop := NewMailbox(0), NewMailbox(0)
	_, _ = bus.Register(uuid.New(), 1, down)
	bus.SetHealthy(false)
	if down.Reason() != ReasonFeedDown {
		t.Fatalf("feed down closed with %v", down.Reason())
	}
	bus.SetHealthy(true)
	_, _ = bus.Register(uuid.New(), 2, stop)
	bus.Shutdown()
	if stop.Reason() != ReasonShutdown || bus.Healthy() {
		t.Fatalf("shutdown: reason %v, healthy %v", stop.Reason(), bus.Healthy())
	}
}

func TestBusSubscribedOrganizations(t *testing.T) {
	bus := healthyBus()
	orgA, orgB := uuid.New(), uuid.New()
	_, _ = bus.Register(orgA, 1, subscribed(ChannelStreams))
	_, _ = bus.Register(orgB, 2, subscribed(ChannelPeople))
	got := bus.SubscribedOrganizations(ChannelStreams)
	if len(got) != 1 || got[0] != orgA {
		t.Fatalf("organizations following streams = %v", got)
	}
}

func TestParseNotification(t *testing.T) {
	org := uuid.New()
	ok := func(payload string) bool { _, _, ok := ParseNotification(payload); return ok }
	good := fmt.Sprintf(`{"ch":"org.people","org":%q,"id":"42","rev":"3:x","kind":"updated"}`, org)
	gotOrg, c, valid := ParseNotification(good)
	if !valid || gotOrg != org || c.Channel != ChannelPeople || c.ID != "42" || c.Rev != "3:x" || c.Kind != KindUpdated {
		t.Fatalf("good payload parsed as %v %+v %v", gotOrg, c, valid)
	}
	bad := map[string]string{
		"not json":          `nope`,
		"no organization":   `{"ch":"org.people","id":"1","rev":"1","kind":"updated"}`,
		"nil organization":  fmt.Sprintf(`{"ch":"org.people","org":%q,"id":"1","rev":"1","kind":"updated"}`, uuid.Nil),
		"garbage org":       `{"ch":"org.people","org":"not-a-uuid","id":"1","rev":"1","kind":"updated"}`,
		"unknown channel":   fmt.Sprintf(`{"ch":"org.other","org":%q,"id":"1","rev":"1","kind":"updated"}`, org),
		"streams from db":   fmt.Sprintf(`{"ch":"org.streams","org":%q,"id":"1","rev":"1","kind":"updated"}`, org),
		"unknown kind":      fmt.Sprintf(`{"ch":"org.people","org":%q,"id":"1","rev":"1","kind":"exploded"}`, org),
		"empty id":          fmt.Sprintf(`{"ch":"org.people","org":%q,"id":"","rev":"1","kind":"updated"}`, org),
		"oversized id":      fmt.Sprintf(`{"ch":"org.people","org":%q,"id":%q,"rev":"1","kind":"updated"}`, org, strings.Repeat("x", MaxIDLength+1)),
		"oversized revison": fmt.Sprintf(`{"ch":"org.people","org":%q,"id":"1","rev":%q,"kind":"updated"}`, org, strings.Repeat("x", MaxIDLength+1)),
	}
	for name, payload := range bad {
		if ok(payload) {
			t.Errorf("%s: payload was accepted", name)
		}
	}
}
