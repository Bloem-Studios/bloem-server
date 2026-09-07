package transcodenode

import (
	"net/http"
	"reflect"
	"strconv"

	"github.com/Silo-Server/silo-server/internal/workerprotocol"
	"github.com/danielgtaylor/huma/v2"
)

func ProtocolExecutorPreparation(schemas huma.Registry) workerprotocol.Operation {
	shape := schemas.Schema(reflect.TypeFor[ExecutorPreparation](), true, "")
	op := workerprotocol.Operation{
		Listener: "transcode_node", Method: http.MethodPost, Path: "/transcode/prepare", Handler: "(*internal/transcodenode.Server).handlePrepareExecutor",
		AuthClass: "node_bearer", RetrySafety: "non_retryable",
		Description: "Prepare one selected worker's initial video HLS recipe without launching playback or claiming output. Disabled unless executor callbacks are configured. A bounded JSON body must carry the exact executor and selected execution node. Catalog path authority and current configuration are required. Hardware probes and source validation resolve only local execution policy. The reply is input-only: central must publish it immutably before its one bound start. A lost or changed reply grants no authority and must not trigger another route or playback start.",
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {Schema: shape}}},
		Responses:   map[string]*huma.Response{"200": {Description: "Prepared recipe; no execution authority", Content: map[string]*huma.MediaType{"application/json": {Schema: shape}}}},
	}
	for _, status := range []int{400, 401, 403, 409, 422, 503} {
		op.Responses[strconv.Itoa(status)] = &huma.Response{Description: http.StatusText(status), Content: map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: huma.TypeString}}}}
	}
	return op
}
