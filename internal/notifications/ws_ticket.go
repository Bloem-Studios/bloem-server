package notifications

import (
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/redis/go-redis/v9"
)

// NewTicketStore remains the notifications composition seam while the actual
// ticket authority lives in auth and is shared by every audience.
func NewTicketStore(redisClient *redis.Client) auth.AudienceTicketStore {
	return auth.NewAudienceTicketStore(redisClient)
}
