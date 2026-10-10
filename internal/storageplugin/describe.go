package storageplugin

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// Describe obtains a completed, bounded response for this caller. Runtime
// clients also bind the RPC to their generation's lifetime. The returned copy
// belongs to the consumer; a response accompanied by an RPC error is unusable.
func Describe(ctx context.Context, provider storagev1.StorageProviderClient) (*storagev1.DescribeResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, ErrUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	description, err := provider.Describe(bounded, &storagev1.DescribeRequest{}, grpc.MaxCallRecvMsgSize(1<<20))
	if err != nil {
		if cause := bounded.Err(); cause != nil {
			return nil, errors.Join(err, cause)
		}
		return nil, err
	}
	if err = bounded.Err(); err != nil {
		return nil, err
	}
	if err = validateDescription(description); err != nil {
		return nil, err
	}
	return proto.CloneOf(description), nil
}

func validIdentity(id string) bool {
	return id != "" && len(id) <= 1024 && utf8.ValidString(id) && !strings.ContainsRune(id, 0)
}

// Empty source lists are valid for an unconfigured provider. Every advertised
// identity must still be usable and unambiguous, including unselected sources.
// Roots belong to each source's namespace and may repeat across sources.
func validateDescription(description *storagev1.DescribeResponse) error {
	if description == nil || description.GetRevision() != 1 || len(description.GetSources()) > 128 || proto.Size(description) > 1<<20 {
		return ErrUnavailable
	}
	seen := make(map[string]struct{}, len(description.GetSources()))
	for _, source := range description.GetSources() {
		if source == nil || !validIdentity(source.GetId()) || !validIdentity(source.GetRootEntryId()) {
			return ErrUnavailable
		}
		if _, duplicate := seen[source.GetId()]; duplicate {
			return ErrUnavailable
		}
		seen[source.GetId()] = struct{}{}
	}
	return nil
}

// ValidateSourceDescription admits only the retained source/root pair with
// revision-pinned reads. IDs are opaque and compared byte for byte. This checks
// advertised capability, not whether the backend really preserves revisions.
func ValidateSourceDescription(description *storagev1.DescribeResponse, sourceID, rootID string) error {
	if err := validateDescription(description); err != nil {
		return err
	}
	if !validIdentity(sourceID) || !validIdentity(rootID) {
		return ErrUnavailable
	}
	for _, source := range description.GetSources() {
		if source.GetId() == sourceID {
			if source.GetRootEntryId() != rootID || !source.GetRevisionPinnedReads() {
				return ErrUnavailable
			}
			return nil
		}
	}
	return ErrUnavailable
}
