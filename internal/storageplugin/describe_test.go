package storageplugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

func sourceDescription() *storagev1.DescribeResponse {
	return &storagev1.DescribeResponse{Revision: 1, Sources: []*storagev1.Source{{Id: "books", RootEntryId: "root", RevisionPinnedReads: true}}}
}

func TestDescribeSourceAdmission(t *testing.T) {
	for _, kind := range []string{"nil", "empty", "protocol", "source", "root", "unpinned", "duplicate", "duplicate-other", "nil-source", "empty-id", "empty-root", "nul-id", "nul-root", "utf8-id", "utf8-root", "long-id", "long-root", "source-bound", "payload-bound", "retained-empty-id", "retained-empty-root"} {
		t.Run(kind, func(t *testing.T) {
			d := sourceDescription()
			id, root := "books", "root"
			switch kind {
			case "nil":
				d = nil
			case "empty":
				d.Sources = nil
			case "protocol":
				d.Revision = 2
			case "source":
				d.Sources[0].Id = "other"
			case "root":
				d.Sources[0].RootEntryId = "other"
			case "unpinned":
				d.Sources[0].RevisionPinnedReads = false
			case "duplicate":
				d.Sources = append(d.Sources, proto.CloneOf(d.Sources[0]))
			case "duplicate-other":
				d.Sources = append(d.Sources, &storagev1.Source{Id: "other", RootEntryId: "root"}, &storagev1.Source{Id: "other", RootEntryId: "different"})
			case "nil-source":
				d.Sources = append(d.Sources, nil)
			case "empty-id":
				d.Sources = append(d.Sources, &storagev1.Source{RootEntryId: "root"})
			case "empty-root":
				d.Sources = append(d.Sources, &storagev1.Source{Id: "other"})
			case "nul-id":
				d.Sources = append(d.Sources, &storagev1.Source{Id: "other\x00", RootEntryId: "root"})
			case "nul-root":
				d.Sources[0].RootEntryId = "root\x00"
			case "utf8-id":
				d.Sources[0].Id = string([]byte{0xff})
			case "utf8-root":
				d.Sources[0].RootEntryId = string([]byte{0xff})
			case "long-id":
				d.Sources[0].Id = strings.Repeat("i", 1025)
			case "long-root":
				d.Sources[0].RootEntryId = strings.Repeat("r", 1025)
			case "source-bound":
				for i := range 128 {
					d.Sources = append(d.Sources, &storagev1.Source{Id: fmt.Sprint(i), RootEntryId: "root"})
				}
			case "payload-bound":
				d.Sources[0].Name = strings.Repeat("n", 1<<20)
			case "retained-empty-id":
				id = ""
			case "retained-empty-root":
				root = ""
			}
			if err := ValidateSourceDescription(d, id, root); err == nil {
				t.Fatal("invalid Describe source admitted")
			}
		})
	}
}

func TestDescribeIdentityIsOpaque(t *testing.T) {
	id, root := " books ", "//../ root /"
	d := &storagev1.DescribeResponse{Revision: 1, Sources: []*storagev1.Source{{Id: id, RootEntryId: root, RevisionPinnedReads: true}, {Id: "other", RootEntryId: root}}}
	if err := ValidateSourceDescription(d, id, root); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSourceDescription(d, strings.TrimSpace(id), root); err == nil {
		t.Fatal("identity was normalized")
	}
	if err := ValidateSourceDescription(d, id, " root /"); err == nil {
		t.Fatal("root was normalized")
	}
	id, root = strings.Repeat("i", 1024), strings.Repeat("r", 1024)
	d.Sources = []*storagev1.Source{{Id: id, RootEntryId: root, RevisionPinnedReads: true}}
	if err := ValidateSourceDescription(d, id, root); err != nil {
		t.Fatal(err)
	}
	if err := validateDescription(&storagev1.DescribeResponse{Revision: 1}); err != nil {
		t.Fatal("unconfigured provider rejected", err)
	}
}

type describeClient struct {
	storagev1.StorageProviderClient
	call func(context.Context) (*storagev1.DescribeResponse, error)
}

func (c describeClient) Describe(ctx context.Context, _ *storagev1.DescribeRequest, _ ...grpc.CallOption) (*storagev1.DescribeResponse, error) {
	return c.call(ctx)
}

func TestDescribeRequiresCompletedLiveResponse(t *testing.T) {
	terminal := errors.New("terminal RPC failure")
	for _, kind := range []string{"terminal-error", "caller-canceled", "deadline"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var want error = terminal
			if kind == "deadline" {
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
				want = context.DeadlineExceeded
			}
			client := describeClient{call: func(context.Context) (*storagev1.DescribeResponse, error) {
				if kind == "caller-canceled" {
					cancel()
					return sourceDescription(), nil
				}
				return sourceDescription(), terminal
			}}
			if kind == "caller-canceled" {
				want = context.Canceled
			}
			d, err := Describe(ctx, client)
			if d != nil || !errors.Is(err, want) {
				t.Fatalf("accepted incomplete/canceled Describe: %v %v", d, err)
			}
		})
	}
}

func TestDescribeOwnsBoundedResponse(t *testing.T) {
	original := sourceDescription()
	d, err := Describe(t.Context(), describeClient{call: func(ctx context.Context) (*storagev1.DescribeResponse, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 10*time.Second {
			t.Fatal("Describe is not bounded")
		}
		return original, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	original.Sources[0].RootEntryId = "mutated"
	if err := ValidateSourceDescription(d, "books", "root"); err != nil {
		t.Fatal("provider retained ownership of admitted description", err)
	}
}

func TestDescribePreservesRPCTerminalErrorAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	terminal := errors.New("terminal RPC failure")
	d, err := Describe(ctx, describeClient{call: func(context.Context) (*storagev1.DescribeResponse, error) {
		cancel()
		return sourceDescription(), terminal
	}})
	if d != nil || !errors.Is(err, terminal) || !errors.Is(err, context.Canceled) {
		t.Fatalf("lost terminal error or cancellation: response=%v err=%v", d, err)
	}
}
