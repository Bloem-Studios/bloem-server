package preview

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// ConsumeTicket records a ticket id as used, inside the exchange's
// transaction. A second use of the same id, on any node or after a restart,
// is refused.
func ConsumeTicket(ctx context.Context, tx pgx.Tx, id string, expiresAt time.Time) error {
	tag, err := tx.Exec(ctx, `INSERT INTO bloem_preview_ticket_uses (jti, expires_at)
		VALUES ($1, $2) ON CONFLICT (jti) DO NOTHING`, id, expiresAt)
	if err != nil {
		return fmt.Errorf("recording ticket use: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: already used", ErrTicketInvalid)
	}
	return nil
}

// SweepTicketUses deletes uses whose tickets have expired; a replay of one
// fails verification anyway.
func SweepTicketUses(ctx context.Context, db execer) (int64, error) {
	tag, err := db.Exec(ctx, `DELETE FROM bloem_preview_ticket_uses WHERE expires_at < now()`)
	if err != nil {
		return 0, fmt.Errorf("sweeping ticket uses: %w", err)
	}
	return tag.RowsAffected(), nil
}
