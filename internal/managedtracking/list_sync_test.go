package managedtracking

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	lists "github.com/Silo-Server/silo-server/internal/managedtracking/listwire"
)

func TestListBrokerAbsentBackendAndRegistration(t *testing.T) {
	for _, plugin := range []string{"unrelated", "bloem.pastime"} {
		t.Run(plugin, func(t *testing.T) {
			l := bufconn.Listen(1 << 20)
			g := grpc.NewServer()
			(&Service{}).RegisterBroker(g, plugin, 19)
			go func() { _ = g.Serve(l) }()
			t.Cleanup(g.Stop)
			c, err := grpc.NewClient("passthrough:///test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return l.Dial() }))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { c.Close() })
			_, err = lists.NewListHostClient(c).Info(t.Context(), &lists.InfoRequest{})
			if status.Code(err) != codes.Unimplemented {
				t.Fatal("incomplete capability advertised", err)
			}
		})
	}
}
func TestListBrokerBindsInstallation(t *testing.T) {
	b := &listBroker{service: &Service{}, installation: 19}
	if _, err := b.Poll(t.Context(), &lists.PollRequest{Limit: 101}); status.Code(err) != codes.FailedPrecondition {
		t.Fatal(err)
	}
	if _, err := b.Stage(t.Context(), &lists.Page{}); status.Code(err) != codes.PermissionDenied {
		t.Fatal(err)
	}
}

type listBackendFixture struct {
	profile   Profile
	instance  string
	calls     int
	afterPoll func()
}

func (f *listBackendFixture) PollLists(_ context.Context, _ pgx.Tx, installation int, info Info, _ uint32) (*lists.Jobs, error) {
	f.calls++
	if f.afterPoll != nil {
		f.afterPoll()
	}
	if installation < 1 {
		return nil, ErrAuthority
	}
	return &lists.Jobs{Jobs: []*lists.Job{{Id: "job", InstanceId: f.instance, AccountId: f.profile.AccountID, ProfileId: f.profile.ProfileID, Generation: 1, LeaseToken: "lease", ExpiresAt: timestamppb.New(time.Now().Add(time.Minute))}}}, nil
}
func (f *listBackendFixture) ResolvedList(context.Context, pgx.Tx, int, Info, *lists.Resolution) error {
	return ErrSnapshot
}
func (f *listBackendFixture) StageList(context.Context, pgx.Tx, int, Info, *lists.Page) error {
	return ErrSnapshot
}
func (f *listBackendFixture) CompleteList(context.Context, pgx.Tx, int, Info, *lists.Completion) (*lists.Receipt, error) {
	return nil, ErrSnapshot
}
func (f *listBackendFixture) RejectList(context.Context, pgx.Tx, int, Info, *lists.Failure) error {
	return ErrSnapshot
}
func TestListBrokerGrantAndReturnedInstance(t *testing.T) {
	s, id, p := enrollmentFixture(t)
	info, err := s.GetInfo(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	source := &listBackendFixture{profile: p, instance: info.InstanceID}
	s.SetListBackend(source)
	b := &listBroker{service: s, installation: id}
	if _, err = b.Poll(t.Context(), &lists.PollRequest{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	source.instance = "forged"
	if _, err = b.Poll(t.Context(), &lists.PollRequest{Limit: 1}); status.Code(err) != codes.FailedPrecondition {
		t.Fatal("forged instance published", err)
	}
	if _, err = s.Pool.Exec(t.Context(), `UPDATE bloem_managed_grants SET active=false WHERE installation_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Poll(t.Context(), &lists.PollRequest{Limit: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("revoked installation reached backend", err)
	}
	if source.calls != 2 {
		t.Fatal("unauthorized callback", source.calls)
	}
}

func TestListBrokerGrantIsFencedThroughCommit(t *testing.T) {
	s, id, p := enrollmentFixture(t)
	info, err := s.GetInfo(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	source := &listBackendFixture{profile: p, instance: info.InstanceID}
	source.afterPoll = func() {
		tx, err := s.Pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(t.Context())
		if _, err = tx.Exec(t.Context(), `SET LOCAL lock_timeout='100ms'`); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(t.Context(), `UPDATE bloem_managed_grants SET active=false WHERE installation_id=$1`, id)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "55P03" {
			t.Fatal("grant revocation raced database publication", err)
		}
	}
	s.SetListBackend(source)
	if _, err = (&listBroker{service: s, installation: id}).Poll(t.Context(), &lists.PollRequest{Limit: 1}); err != nil {
		t.Fatal(err)
	}
}
func TestListBrokerInstallationDisableDuringCallback(t *testing.T) {
	s, id, p := enrollmentFixture(t)
	info, err := s.GetInfo(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	source := &listBackendFixture{profile: p, instance: info.InstanceID, afterPoll: func() {
		if _, err := s.Pool.Exec(t.Context(), `UPDATE plugin_installations SET enabled=false WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}}
	s.SetListBackend(source)
	if _, err = (&listBroker{service: s, installation: id}).Poll(t.Context(), &lists.PollRequest{Limit: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("disabled installation returned jobs", err)
	}
}
