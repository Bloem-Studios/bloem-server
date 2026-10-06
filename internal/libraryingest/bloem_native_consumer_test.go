package libraryingest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

func TestNativeConsumerDescriptionFailsClosed(t *testing.T) {
	source := storagesource.SourceConfig{ProviderSourceID: "books", RootEntryID: "root"}
	for _, mode := range []string{"false", "wrong-root", "wrong-source", "duplicate", "revision"} {
		t.Run(mode, func(t *testing.T) {
			d := &storagev1.DescribeResponse{Revision: 1, Sources: []*storagev1.Source{{Id: "books", RootEntryId: "root", RevisionPinnedReads: true}}}
			switch mode {
			case "false":
				d.Sources[0].RevisionPinnedReads = false
			case "wrong-root":
				d.Sources[0].RootEntryId = "other"
			case "wrong-source":
				d.Sources[0].Id = "other"
			case "duplicate":
				d.Sources = append(d.Sources, d.Sources[0])
			case "revision":
				d.Revision = 2
			}
			if !errors.Is(nativeConsumerDescription(d, source), storagesource.ErrSourceUnavailable) {
				t.Fatal("unavailable capability/identity accepted")
			}
		})
	}
	if err := nativeConsumerDescription(&storagev1.DescribeResponse{Revision: 1, Sources: []*storagev1.Source{{Id: "books", RootEntryId: "root", RevisionPinnedReads: true}}}, source); err != nil {
		t.Fatal(err)
	}
}

func TestNativeConsumerRenewalFailureCancelsAndJoins(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	failure := errors.New("lease lost")
	stop := startNativeRenewal(ctx, cancel, time.Millisecond, func(context.Context) error { return failure })
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("lease failure did not cancel I/O")
	}
	if !errors.Is(context.Cause(ctx), failure) {
		t.Fatal("lease failure cause lost")
	}
	stop()
}

func TestNativeConsumerStopCancelsInFlightRenewal(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	started, exited := make(chan struct{}), make(chan struct{})
	stop := startNativeRenewal(ctx, cancel, time.Millisecond, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	})
	<-started
	stop()
	select {
	case <-exited:
	default:
		t.Fatal("renewal still running")
	}
	if ctx.Err() != nil {
		t.Fatal("normal renewal stop canceled scan")
	}
}

func TestNativeConsumerStopDoesNotHideLeaseFailure(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	started := make(chan struct{})
	failure := errors.New("actual lease fence lost")
	stop := startNativeRenewal(ctx, cancel, time.Millisecond, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return failure
	})
	<-started
	stop()
	if !errors.Is(context.Cause(ctx), failure) {
		t.Fatal("stopping renewal hid an actual lease failure")
	}
}

func TestNativeConsumerStopDoesNotHideDeadline(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	started := make(chan struct{})
	stop := startNativeRenewal(ctx, cancel, time.Millisecond, func(ctx context.Context) error {
		close(started)
		// Return an actual bounded operation's deadline only after Stop cancels
		// the renewal parent: force the formerly suppressed interleaving.
		<-ctx.Done()
		bounded, release := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer release()
		<-bounded.Done()
		return bounded.Err()
	})
	<-started
	stop()
	if !errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		t.Fatalf("Stop hid deadline failure: %v", context.Cause(ctx))
	}
}

func TestNativeConsumerRetainedLeaseAndClaimIdentity(t *testing.T) {
	binding := storagesource.Binding{ID: uuid.New(), SourceKey: uuid.New(), FolderID: 7}
	source := storagesource.SourceConfig{Key: binding.SourceKey, ConfigurationRevision: 3}
	lease := storagesource.IngestionLease{RunID: uuid.New(), BindingID: binding.ID, SourceKey: source.Key, ConfigurationRevision: 3, Epoch: 2, Owner: "retained"}
	claim := storagesource.IngestionClaim{Lease: lease, Token: uuid.New(), Entry: &storagev1.Entry{Id: "book", Revision: "pinned"}}
	if err := nativeConsumerLease(lease, binding, source); err != nil {
		t.Fatal(err)
	}
	if err := nativeConsumerClaim(claim, lease, binding, source); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"binding", "source", "configuration", "run", "epoch", "owner", "entry", "token"} {
		t.Run(mode, func(t *testing.T) {
			bad := claim
			switch mode {
			case "binding":
				bad.Lease.BindingID = uuid.New()
			case "source":
				bad.Lease.SourceKey = uuid.New()
			case "configuration":
				bad.Lease.ConfigurationRevision++
			case "run":
				bad.Lease.RunID = uuid.New()
			case "epoch":
				bad.Lease.Epoch++
			case "owner":
				bad.Lease.Owner = "other"
			case "entry":
				bad.Entry = nil
			case "token":
				bad.Token = uuid.Nil
			}
			if !errors.Is(nativeConsumerClaim(bad, lease, binding, source), storagesource.ErrStaleLease) {
				t.Fatal("malformed claim accepted")
			}
		})
	}
}

func TestNativeConsumerStopSuppressesOnlyWrappedCancellation(t *testing.T) {
	for _, mode := range []string{"wrapped-cancel", "joined-fence", "joined-deadline"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			started := make(chan struct{})
			fence := errors.New("actual storage fence")
			stop := startNativeRenewal(ctx, cancel, time.Millisecond, func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				switch mode {
				case "joined-fence":
					return errors.Join(fence, ctx.Err())
				case "joined-deadline":
					return errors.Join(context.DeadlineExceeded, ctx.Err())
				default:
					return fmt.Errorf("SQL timeout: context already done: %w", ctx.Err())
				}
			})
			<-started
			stop()
			switch mode {
			case "wrapped-cancel":
				if context.Cause(ctx) != nil {
					t.Fatalf("deliberate wrapped Stop cancellation became failure: %v", context.Cause(ctx))
				}
			case "joined-fence":
				if !errors.Is(context.Cause(ctx), fence) {
					t.Fatal("Stop hid joined genuine fence")
				}
			case "joined-deadline":
				if !errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
					t.Fatal("Stop hid joined deadline")
				}
			}
		})
	}
}
