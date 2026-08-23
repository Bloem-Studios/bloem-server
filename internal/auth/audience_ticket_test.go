package auth

import (
	"context"
	"errors"
	"testing"
)

func TestAudienceTicketIsSingleUseAndRouteBound(t *testing.T) {
	store := NewAudienceTicketStore(nil)
	ticket, _, err := store.Mint(context.Background(), AudienceTicket{
		Audience:   AudienceWatchTogetherWS,
		AccountID:  7,
		ProfileID:  "profile-1",
		ResourceID: "room-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Consume(context.Background(), ticket, AudienceEventsWS, ""); !errors.Is(err, ErrAudienceTicketMismatch) {
		t.Fatalf("wrong audience error = %v", err)
	}
	claims, err := store.Consume(context.Background(), ticket, AudienceWatchTogetherWS, "room-1")
	if err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if claims.AccountID != 7 || claims.ProfileID != "profile-1" {
		t.Fatalf("claims = %#v", claims)
	}
	if _, err := store.Consume(context.Background(), ticket, AudienceWatchTogetherWS, "room-1"); !errors.Is(err, ErrAudienceTicketInvalid) {
		t.Fatalf("replay error = %v", err)
	}
}

func TestAudienceTicketWrongResourceDoesNotConsume(t *testing.T) {
	store := NewAudienceTicketStore(nil)
	ticket, _, err := store.Mint(context.Background(), AudienceTicket{
		Audience:   AudienceWatchTogetherWS,
		AccountID:  7,
		ProfileID:  "profile-1",
		ResourceID: "room-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Consume(context.Background(), ticket, AudienceWatchTogetherWS, "room-2"); !errors.Is(err, ErrAudienceTicketMismatch) {
		t.Fatalf("wrong resource error = %v", err)
	}
	if _, err := store.Consume(context.Background(), ticket, AudienceWatchTogetherWS, "room-1"); err != nil {
		t.Fatalf("correct resource after mismatch: %v", err)
	}
}
