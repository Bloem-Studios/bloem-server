package nativestorage

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

func referenceFixture(t *testing.T) (*SourceBrowser, browseReference, auth.AdminContextClaims, storagesource.SourceConfig) {
	t.Helper()
	cipher, err := secret.New([]byte(strings.Repeat("synthetic-reference-key", 2)))
	if err != nil {
		t.Fatal(err)
	}
	b := &SourceBrowser{cipher: cipher, now: time.Now}
	installation := int64(3)
	a := auth.AdminContextClaims{AccountID: 7, AccountIncarnationID: uuid.New(), Scope: auth.AdminScopePlatform, SessionID: "login", ExpiresAt: time.Now().Add(time.Hour)}
	source := storagesource.SourceConfig{Key: uuid.New(), OwnerID: uuid.New(), InstallationID: &installation, PluginID: "plugin", ProviderSourceID: "books", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true}
	return b, b.newReference(a, source, 1), a, source
}
func TestSelectionReferencesBindPurposeAndAuthority(t *testing.T) {
	b, ref, actor, source := referenceFixture(t)
	token, err := b.sealBrowse(ref, selectionPurpose)
	if err != nil {
		t.Fatal(err)
	}
	var opened browseReference
	if err := b.openBrowse(token, selectionPurpose, &opened); err != nil {
		t.Fatal(err)
	}
	if err := b.validateReference(opened, actor, source, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.openBrowse(token, cursorPurpose, &opened); err == nil {
		t.Fatal("selection reference accepted as a cursor")
	}
	for _, change := range []string{"kind", "generation", "actor", "root", "expiry"} {
		t.Run(change, func(t *testing.T) {
			copy := ref
			copy.Ancestry = append([]string(nil), ref.Ancestry...)
			switch change {
			case "kind":
				copy.Kind = "file"
			case "generation":
				copy.Generation++
			case "actor":
				copy.ActorDigest = "other-login"
			case "root":
				copy.Ancestry[0] = "other"
			case "expiry":
				copy.ExpiresAt = time.Now().Add(-time.Second).Unix()
			}
			if err := b.validateReference(copy, actor, source, 1); err == nil {
				t.Fatal("invalid authority accepted")
			}
		})
	}
}
func TestSelectionReferencesConcurrentIssue(t *testing.T) {
	b, ref, actor, source := referenceFixture(t)
	var wg sync.WaitGroup
	results := make(chan string, 64)
	failures := make(chan error, 64)
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := b.sealBrowse(ref, selectionPurpose)
			if err != nil {
				failures <- err
				return
			}
			var opened browseReference
			if err := b.openBrowse(token, selectionPurpose, &opened); err != nil {
				failures <- err
				return
			}
			if err := b.validateReference(opened, actor, source, 1); err != nil {
				failures <- err
				return
			}
			results <- token
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for token := range results {
		if seen[token] {
			t.Fatal("nonce reused")
		}
		seen[token] = true
	}
	if len(seen) != 64 {
		t.Fatal("concurrent references missing")
	}
}

func TestSelectionReferencesRejectUnserializableAuthority(t *testing.T) {
	b, _, actor, source := referenceFixture(t)
	actor.ExpiresAt = time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
	ref := b.newReference(actor, source, 1)
	if err := b.validateReference(ref, actor, source, 1); err == nil {
		t.Fatal("unserializable actor accepted by reference validation")
	}
	if _, err := b.directory(ref, "root", ""); err == nil {
		t.Fatal("selection reference issued for unserializable authority")
	}
}
