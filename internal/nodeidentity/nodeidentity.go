// Package nodeidentity carries this process's node instance identity.
//
// A node's heartbeat row is written from more than one place -- the periodic
// heartbeat writer and the SQLite cluster guard both upsert it -- and both must
// agree on the instance they are writing, for two reasons:
//
//   - the membership policy rollout keys a capable node by (node_id,
//     instance_id), so a value that changed per write would register a new node
//     on every heartbeat and never finish rolling out; and
//   - the heartbeat delete fence only admits a session that names the instance
//     the row carries, so a process that wrote one identity and deleted with
//     another could never retire its own heartbeat.
//
// One value per process satisfies both.
package nodeidentity

import (
	"sync"

	"github.com/google/uuid"
)

var instanceID = sync.OnceValue(func() string { return uuid.NewString() })

// InstanceID returns the identity of this process, stable for its lifetime.
func InstanceID() string { return instanceID() }
