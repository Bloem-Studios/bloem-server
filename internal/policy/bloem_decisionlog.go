package policy

import (
	"github.com/google/uuid"
)

func nullableUUID(value uuid.UUID) any {
	if value == uuid.Nil {
		return nil
	}
	return value
}
