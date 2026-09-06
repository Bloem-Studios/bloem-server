package proxy

import (
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/workerprotocol"
	"github.com/danielgtaylor/huma/v2"
)

// ProtocolReads describes the retained reads using the same DTOs their handlers
// encode. The bearer middleware and worker paths remain owned by this listener.
func ProtocolReads(schemas huma.Registry) []workerprotocol.Operation {
	return []workerprotocol.Operation{
		workerprotocol.JSONRead[playback.HWAccelInfo](schemas, "proxy", "/hw-capabilities", "(*internal/proxy.Server).handleHWCapabilities", 401, 503),
		workerprotocol.JSONRead[statusResponse](schemas, "proxy", "/status", "(*internal/proxy.Server).handleStatus", 401),
	}
}
