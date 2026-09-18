package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
)

func TestBloemProfileCredentialStaleChangesPreserveNewCredentialAndSessions(t *testing.T) {
	ctx := context.Background()
	service := newProfileCredentialService(t)
	for _, operation := range []string{"clear", "set", "empty-pair"} {
		t.Run(operation, func(t *testing.T) {
			account, profile := newProfileCredentialFixture(t, service.pool, "stale-"+operation)
			email := operation + "@example.test"
			initial, err := service.Status(ctx, account, profile)
			if err != nil || initial.CredentialRevision != 1 {
				t.Fatalf("initial status=%+v err=%v", initial, err)
			}
			if err := service.SetAtRevision(ctx, account, profile, email, "first-password", 1); err != nil {
				t.Fatal(err)
			}
			reviewed, err := service.Status(ctx, account, profile)
			if err != nil || reviewed.CredentialRevision != 2 {
				t.Fatalf("reviewed status=%+v err=%v", reviewed, err)
			}
			sessions := NewSessionRepository(service.pool)
			oldSession := newBloemCredentialRevisionSession(t, service, email, "first-password")
			accountSession := uuid.NewString()
			if err := sessions.Create(ctx, models.AuthSession{ID: accountSession, UserID: account, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			if err := service.SetAtRevision(ctx, account, profile, email, "second-password", reviewed.CredentialRevision); err != nil {
				t.Fatal(err)
			}
			assertBloemCredentialSessionRevoked(t, sessions, oldSession, true)
			currentSession := newBloemCredentialRevisionSession(t, service, email, "second-password")

			switch operation {
			case "clear":
				err = service.ClearAtRevision(ctx, account, profile, reviewed.CredentialRevision)
			case "set":
				err = service.SetAtRevision(ctx, account, profile, "stale@example.test", "stale-password", reviewed.CredentialRevision)
			case "empty-pair":
				err = service.SetAtRevision(ctx, account, profile, "", "", reviewed.CredentialRevision)
			}
			if !errors.Is(err, ErrProfileCredentialRevisionConflict) {
				t.Fatalf("stale %s error=%v, want revision conflict", operation, err)
			}
			status, err := service.Status(ctx, account, profile)
			if err != nil || !status.Configured || status.LoginEmail != email || status.CredentialRevision != 3 {
				t.Fatalf("stale %s changed status=%+v err=%v", operation, status, err)
			}
			if _, err := service.Authenticate(ctx, email, "second-password", DeviceClaim{}); err != nil {
				t.Fatalf("stale write replaced current password or registry entry: %v", err)
			}
			assertBloemCredentialSessionRevoked(t, sessions, currentSession, false)
			assertBloemCredentialSessionRevoked(t, sessions, accountSession, false)
			if err := service.ClearAtRevision(ctx, account, profile, status.CredentialRevision); err != nil {
				t.Fatal(err)
			}
			status, err = service.Status(ctx, account, profile)
			if err != nil || status.Configured || status.LoginEmail != "" || status.CredentialRevision != 4 {
				t.Fatalf("reviewed clear status=%+v err=%v", status, err)
			}
			assertBloemCredentialSessionRevoked(t, sessions, currentSession, true)
			assertBloemCredentialSessionRevoked(t, sessions, accountSession, false)
		})
	}
}

func TestBloemProfileCredentialConcurrentReviewedWritesAllowOneWinner(t *testing.T) {
	service := newProfileCredentialService(t)
	for _, second := range []string{"set", "clear"} {
		t.Run("set-versus-"+second, func(t *testing.T) {
			account, profile := newProfileCredentialFixture(t, service.pool, "concurrent-"+second)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := service.SetAtRevision(ctx, account, profile, "initial-"+second+"@example.test", "first-password", 1); err != nil {
				t.Fatal(err)
			}
			// Hold the row until both writers actually wait on database locks.
			// A revision precheck outside that lock lets both writes succeed.
			blocker, err := service.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback(context.Background()) }()
			if _, err := blocker.Exec(ctx, `SELECT 1 FROM user_profiles WHERE user_id=$1 AND id=$2 FOR UPDATE`, account, profile); err != nil {
				t.Fatal(err)
			}
			type outcome struct {
				name string
				err  error
			}
			results := make(chan outcome, 2)
			go func() {
				results <- outcome{"first", service.SetAtRevision(ctx, account, profile, "first-"+second+"@example.test", "first-replacement", 2)}
			}()
			go func() {
				if second == "clear" {
					results <- outcome{"second", service.ClearAtRevision(ctx, account, profile, 2)}
				} else {
					results <- outcome{"second", service.SetAtRevision(ctx, account, profile, "second-"+second+"@example.test", "second-replacement", 2)}
				}
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var blocked int
				if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND cardinality(pg_blocking_pids(pid)) > 0`).Scan(&blocked); err != nil {
					t.Fatalf("observe concurrent writers: %v", err)
				}
				if blocked == 2 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("both credential writers did not reach the row lock")
				case <-ticker.C:
				}
			}
			if err := blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			var winner string
			var successes, conflicts int
			for range 2 {
				select {
				case result := <-results:
					switch {
					case result.err == nil:
						successes++
						winner = result.name
					case errors.Is(result.err, ErrProfileCredentialRevisionConflict):
						conflicts++
					default:
						t.Fatalf("concurrent write error: %v", result.err)
					}
				case <-ctx.Done():
					t.Fatal("concurrent credential writes did not finish")
				}
			}
			if successes != 1 || conflicts != 1 {
				t.Fatalf("successes=%d conflicts=%d, want one each", successes, conflicts)
			}
			status, err := service.Status(ctx, account, profile)
			if err != nil || status.CredentialRevision != 3 {
				t.Fatalf("winning status=%+v err=%v", status, err)
			}
			if winner == "second" && second == "clear" {
				if status.Configured || status.LoginEmail != "" {
					t.Fatalf("clear won but credentials remain: %+v", status)
				}
			} else if !status.Configured || status.LoginEmail != winner+"-"+second+"@example.test" {
				t.Fatalf("winner=%s but status=%+v", winner, status)
			}
		})
	}
}

func TestBloemProfileCredentialRevisionValidationAndOwnership(t *testing.T) {
	ctx := context.Background()
	service := newProfileCredentialService(t)
	account, profile := newProfileCredentialFixture(t, service.pool, "review-validation")
	for _, revision := range []int64{0, -1} {
		if err := service.SetAtRevision(ctx, account, profile, "reader@example.test", "password", revision); !errors.Is(err, ErrInvalidProfileCredentialRevision) {
			t.Fatalf("invalid set revision: %v", err)
		}
		if err := service.ClearAtRevision(ctx, account, profile, revision); !errors.Is(err, ErrInvalidProfileCredentialRevision) {
			t.Fatalf("invalid clear revision: %v", err)
		}
	}
	if err := service.SetAtRevision(ctx, account+10000, profile, "reader@example.test", "password", 1); !errors.Is(err, ErrProfileCredentialNotFound) {
		t.Fatalf("foreign set: %v", err)
	}
	if err := service.ClearAtRevision(ctx, account+10000, profile, 1); !errors.Is(err, ErrProfileCredentialNotFound) {
		t.Fatalf("foreign clear: %v", err)
	}
	status, err := service.Status(ctx, account, profile)
	if err != nil || status.Configured || status.CredentialRevision != 1 {
		t.Fatalf("rejected writes changed status=%+v err=%v", status, err)
	}
}

func newBloemCredentialRevisionSession(t *testing.T, credentials profileCredentialTestService, email, password string) string {
	t.Helper()
	ctx := context.Background()
	subject, err := credentials.Authenticate(ctx, email, password, DeviceClaim{ID: "revision-test-device"})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	err = NewSessionRepository(credentials.pool).CreateProfileSessionIfCurrent(ctx, models.AuthSession{
		ID: id, UserID: subject.AccountID, DeviceID: subject.Device.ID,
		ExpiresAt: time.Now().Add(time.Hour), ProfileID: &subject.ProfileID,
		ProfileCredentialRevision: &subject.CredentialRevision, AuthMethod: AuthMethodDirectProfile,
	}, subject)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func assertBloemCredentialSessionRevoked(t *testing.T, sessions *SessionRepository, id string, revoked bool) {
	t.Helper()
	session, err := sessions.GetByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if (session.RevokedAt != nil) != revoked {
		t.Fatalf("session revoked=%t want %t", session.RevokedAt != nil, revoked)
	}
}
