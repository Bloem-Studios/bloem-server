package access

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// bloemGroupPolicyByName is GroupPolicyByName within one organization. Group
// names are unique per organization in Bloem, so a name alone can match
// another organization's group. A restore names the organization an account
// is restored into as the creation organization (tenancy.WithCreationOrganization);
// without one it leaves the lookup to Silo's.
func bloemGroupPolicyByName(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, name string) (*GroupPolicy, bool, error) {
	organizationID, ok := tenancy.CreationOrganization(ctx)
	if !ok {
		return nil, false, nil
	}
	policy, err := scanGroupPolicy(db.QueryRow(ctx, `
		SELECT g.id, g.library_ids, g.max_playback_quality, g.download_allowed,
			g.download_transcode_allowed, g.transcode_allowed, g.audio_transcode_allowed,
			g.max_streams, g.max_transcodes, g.max_remote_stream_bitrate_kbps, g.max_local_stream_bitrate_kbps,
			g.max_profiles, g.allowed_permissions, g.requests_allowed
		FROM access_groups g WHERE g.organization_id = $1 AND g.name = $2`, organizationID, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, true, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("loading access group %q: %w", name, err)
	}
	return policy, true, nil
}
