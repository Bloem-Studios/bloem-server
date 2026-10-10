package bookrequests

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeISBNAndIdentity(t *testing.T) {
	a, err := normalizeInput(CreateInput{Title: " A book ", Author: "Author", ISBN: "0-306-40615-2"})
	if err != nil || a.ISBN != "9780306406157" {
		t.Fatalf("ISBN-10: %+v %v", a, err)
	}
	b, err := normalizeInput(CreateInput{Title: "A book", Author: "Author", ISBN: "9780306406157"})
	if err != nil || a.identity != b.identity {
		t.Fatalf("ISBN identity differs: %+v %v", b, err)
	}
	for _, isbn := range []string{"9780306406158", "0306406153", "garbage"} {
		if _, err := normalizeInput(CreateInput{Title: "Book", ISBN: isbn}); !errors.Is(err, ErrInvalid) {
			t.Errorf("ISBN %s: %v", isbn, err)
		}
	}
	if _, err := normalizeInput(CreateInput{Title: "Book", ASIN: "invalid"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestRequesterIsolationIncludesProfileAndOrganization(t *testing.T) {
	org := uuid.New()
	r := Request{OrganizationID: org, RequestedByUserID: 7, RequestedByProfileID: "primary"}
	allowed := Viewer{OrganizationID: org, UserID: 7, ProfileID: "primary"}
	if !canRead(allowed, r) {
		t.Fatal("requester denied")
	}
	for _, v := range []Viewer{
		{OrganizationID: org, UserID: 7, ProfileID: "child"},
		{OrganizationID: uuid.New(), UserID: 7, ProfileID: "primary"},
		{OrganizationID: uuid.New(), UserID: 1, Admin: true},
		{OrganizationID: org, UserID: 0, Admin: true},
	} {
		if canRead(v, r) {
			t.Fatalf("unauthorized viewer: %+v", v)
		}
	}
	if !canRead(Viewer{OrganizationID: org, UserID: 1, Admin: true}, r) {
		t.Fatal("organization admin denied")
	}
}
