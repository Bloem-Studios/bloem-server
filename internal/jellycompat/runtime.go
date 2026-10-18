package jellycompat

import (
	"time"

	"github.com/google/uuid"
)

var (
	timeNow       = time.Now
	uuidNewString = uuid.NewString
)
