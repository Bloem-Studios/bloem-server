//go:build integration

package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Silo-Server/silo-server/internal/listcollections"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	lists "github.com/Silo-Server/silo-server/internal/managedtracking/listwire"
)

func TestListFollowBackendPersistsResolvedPreview(t *testing.T) {
	s, subject, _ := listPublicationFixture(t)
	ctx := t.Context()
	preview, err := s.QueuePreview(ctx, subject, listcollections.PreviewInput{URL: "https://pastime.example/lists/1"})
	if err != nil {
		t.Fatal(err)
	}
	backend := &listCollectionBackend{service: s}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	info := managedtracking.Info{InstanceID: subject.InstanceID}
	jobs, err := backend.PollLists(ctx, tx, subject.InstallationID, info, 3)
	if err != nil || len(jobs.Jobs) != 1 {
		t.Fatalf("broker claims: %v %v", jobs, err)
	}
	j := jobs.Jobs[0]
	if j.Id != preview.ID || j.InstanceId != subject.InstanceID || j.Locator != "https://pastime.example/lists/1" || j.AccountId == "" || j.ExpiresAt == nil || j.LeaseToken == "" {
		t.Fatal("claim fields changed", j)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	err = backend.ResolvedList(ctx, tx, subject.InstallationID, info, &lists.Resolution{JobId: j.Id, LeaseToken: j.LeaseToken, Generation: j.Generation, SourceId: "personal:stable", Name: "Preview", Kind: "manual", Count: 0, ResolutionToken: "synthetic-grant", SigningKey: base64.RawURLEncoding.EncodeToString(pub), ExpiresAt: timestamppb.New(time.Now().Add(time.Minute))})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateBinding(ctx, subject, listcollections.CreateInput{PreviewID: j.Id, Scope: "personal", Name: "Linked through broker"}); err != nil {
		t.Fatal(err)
	}
}
