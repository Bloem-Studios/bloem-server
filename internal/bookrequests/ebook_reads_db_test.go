package bookrequests

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestEbookCapabilityAndHistoryDatabase(t *testing.T) {
	s, viewer, pool := databaseFixture(t)
	capability, err := s.CapabilityEbook(t.Context(), viewer)
	if err != nil || !capability.Available || capability.Enabled || capability.CanRequest || capability.ManualFulfilment || capability.FulfilmentBackend != "unconfigured" {
		t.Fatalf("default-off ebook capability: %+v %v", capability, err)
	}
	setEbookOptIn(t, pool, "true")
	book, _, err := s.CreateEbook(t.Context(), viewer, EbookCreateInput{Title: "Edition", ISBN: "9780306406157"}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	other := otherEbookViewer(t, pool, viewer)
	otherBook, _, err := s.CreateEbook(t.Context(), other, EbookCreateInput{Title: "Other edition", ISBN: "9780131103627"}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	_ = submit(t, s, viewer, CreateInput{Title: "Audio", Author: "Author"})
	capability, err = s.CapabilityEbook(t.Context(), viewer)
	if err != nil || !capability.Enabled || !capability.CanRequest || capability.Used != 1 || len(capability.MediaTypes) != 1 || capability.MediaTypes[0] != "book" {
		t.Fatalf("ebook capability/quota crossed lane: %+v %v", capability, err)
	}
	setEbookOptIn(t, pool, "false")
	rows, err := s.ListEbooks(t.Context(), viewer, 100, 0)
	if err != nil || len(rows) != 1 || rows[0].ID != book.ID {
		t.Fatalf("ebook history after opt-out: %+v %v", rows, err)
	}
	admin := viewer
	admin.Admin = true
	rows, err = s.ListEbooks(t.Context(), admin, 100, 0)
	if err != nil || len(rows) != 2 || rows[0].ID != otherBook.ID {
		t.Fatalf("admin ebook queue: %+v %v", rows, err)
	}
	if _, err = s.ListEbooks(t.Context(), admin, 100, -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative pagination accepted: %v", err)
	}
	bookSQL(t, pool, `UPDATE users SET enabled=false WHERE id=$1`, viewer.UserID)
	if _, err = s.ListEbooks(t.Context(), admin, 100, 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("disabled admin retained queue: %v", err)
	}
	if _, err = s.CapabilityEbook(t.Context(), admin); !errors.Is(err, ErrForbidden) {
		t.Fatalf("disabled admin retained capability: %v", err)
	}
}
