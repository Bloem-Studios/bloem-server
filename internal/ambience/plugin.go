package ambience

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Silo-Server/silo-server/internal/bloempresentation"
)

const EvaluationOperation = "ambience.evaluate"

// Caller is the bounded supervised presentation worker transport.
type Caller interface {
	Call(context.Context, string, any, any) error
}

type PluginEvaluator struct{ caller Caller }

// NewPluginEvaluator delegates every evaluation to the configured worker. Worker
// errors propagate to the host; there is no in-process fallback at this boundary.
func NewPluginEvaluator(caller Caller) *PluginEvaluator { return &PluginEvaluator{caller: caller} }

func (p *PluginEvaluator) Evaluate(ctx context.Context, req EvaluationRequest) (EvaluationResponse, error) {
	if err := ctx.Err(); err != nil {
		return EvaluationResponse{}, err
	}
	if p == nil || p.caller == nil {
		return EvaluationResponse{}, errors.New("ambience: plugin transport is unavailable")
	}
	var result EvaluationResponse
	if err := p.caller.Call(ctx, EvaluationOperation, req, &result); err != nil {
		return EvaluationResponse{}, fmt.Errorf("ambience: plugin: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return EvaluationResponse{}, err
	}
	return result, nil
}

// RunPlugin serves the versioned presentation protocol on the supplied streams.
// It has no database, authorization, storage, signing, or settings dependencies.
func RunPlugin(reader io.Reader, writer io.Writer) error {
	return bloempresentation.Serve(reader, writer, func(operation string, payload json.RawMessage) (any, error) {
		if operation != EvaluationOperation {
			return nil, fmt.Errorf("ambience: unsupported operation %q", operation)
		}
		var req EvaluationRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("ambience: decode evaluation: %w", err)
		}
		return (Engine{}).Evaluate(context.Background(), req)
	})
}
