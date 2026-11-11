package apiv2

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/watchdoc"
)

// The Watch surface and offline progress sync: what a television renders, and
// how a client reconciles playback positions it recorded while offline.
//
// Both are Bloem-only. The Watch document composes a whole home screen in one
// response rather than making a client assemble it from several catalog calls,
// and the sync endpoint takes a batch with per-item results instead of failing
// the whole request on one bad entry. Neither shape exists upstream.
//
// The Watch bodies reuse watchdoc.Document directly. The sync shapes restate
// handlers' unexported request and response types, held in step by
// TestBloemSyncProgressDocumentMatchesTheServedShape.

// Gate outcomes of the native client group (router_bloem_client.go). Every
// route in that group runs, in order: RequireAuth, the default-organization
// projection (optionalLegacyTenant), the authenticated rate limiter, viewer
// access and the X-Profile-Id requirement. The direct-profile route boundary
// admits none of these routes. Operations whose handlers add a code to the
// same status restate the gate text in their own description.
const (
	bloemClientGate400 = "bad_request: X-Profile-Id is missing."
	bloemClientGate401 = "unauthorized: the bearer credential is missing, invalid or expired, its login session ended, or the account's role changed since the token was issued. tenant_session_required: the account's organization could not be resolved."
	bloemClientGate403 = "forbidden: API key scopes, a direct-profile or preview session, a remote-playback binding or a device binding does not permit this request. password_change_required: the session must replace its temporary password first. organization_suspended: the account's organization is suspended. profile_unverified: the selected profile is PIN-locked and X-Profile-Token is missing or invalid."
	bloemClientGate404 = "not_found: X-Profile-Id names no profile of this account."
	bloemClientGate500 = "internal_error: the viewer's access could not be resolved."
	bloemClientGate503 = "service_unavailable: the credential or the viewer's access policy could not be checked right now; retry after Retry-After. tenant_unavailable: tenant authorization is unavailable."
)

// bloemClientDocumentOp declares a chi route of the native client group with
// the gate statuses above. Callers override a status their handler extends by
// declaring it again with bloemDocumentErrors.
func bloemClientDocumentOp(reg *Registry, method, path, id, tag, summary string, status int) huma.Operation {
	op := bloemChiDocumentOp(reg, method, path, id, tag, summary, "bearerAuth", status)
	op.Parameters = append(op.Parameters,
		&huma.Param{Name: "X-Profile-Id", In: "header", Required: true, Description: "The household profile acting for this request; it must belong to the authenticated account.", Schema: &huma.Schema{Type: "string"}},
		&huma.Param{Name: "X-Profile-Token", In: "header", Description: "Verification proof for a PIN-locked profile; required only when the selected profile is locked. API keys skip PIN verification.", Schema: &huma.Schema{Type: "string"}},
	)
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
		400: bloemClientGate400,
		401: bloemClientGate401,
		403: bloemClientGate403,
		404: bloemClientGate404,
		500: bloemClientGate500,
		503: bloemClientGate503,
	})
	op.Responses["503"].Headers = map[string]*huma.Header{
		"Retry-After": {Schema: &huma.Schema{Type: "integer"}, Description: "Seconds before retrying; sent with service_unavailable."},
	}
	bloemDocumentRateLimit(reg, &op)
	return op
}

// BloemWatchDocumentOutput is a composed Watch document.
type BloemWatchDocumentOutput struct {
	CacheControl string `header:"Cache-Control" doc:"private, no-store"`
	Body         watchdoc.Document
}

// BloemWatchItemInput names the item to compose a document for.
type BloemWatchItemInput struct {
	ContentID string `path:"content_id" doc:"Catalog content identity. A blank value answers 400."`
}

// BloemWatchSearchInput carries the query a search document is composed for.
type BloemWatchSearchInput struct {
	Query string `query:"q" doc:"Search terms; surrounding whitespace is ignored and a blank value answers 400." required:"true"`
}

func registerBloemWatch(reg *Registry) {
	// Watch and progress sync are mounted only when the deployment has a
	// catalog, media-file repository and profile store; a deployment without
	// them leaves the routes unmounted rather than answering empty.
	{
		op := bloemClientDocumentOp(reg, "GET", "/watch/home", "getBloemWatchHome", "watch",
			"The composed home document for this viewer.", http.StatusOK)
		op.Description = "Composes up to 100 of the most recently added movies and series the selected profile may see, with that profile's progress. Served with Cache-Control: private, no-store."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			500: "internal_error: the viewer's access could not be resolved, or the document could not be composed.",
		})
		registerBloemChiDocument[struct{}, BloemWatchDocumentOutput](reg, op)
	}

	{
		op := bloemClientDocumentOp(reg, "GET", "/watch/items/{content_id}", "getBloemWatchItem", "watch",
			"The composed document for one item.", http.StatusOK)
		op.Description = "An item the selected profile may not see answers 404 exactly like an item that does not exist. Served with Cache-Control: private, no-store."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing or content_id is blank.",
			404: "not_found: the item does not exist or the selected profile may not see it, or X-Profile-Id names no profile of this account.",
			500: "internal_error: the viewer's access could not be resolved, or the document could not be composed.",
		})
		registerBloemChiDocument[BloemWatchItemInput, BloemWatchDocumentOutput](reg, op)
	}

	{
		op := bloemClientDocumentOp(reg, "GET", "/watch/search", "searchBloemWatch", "watch",
			"A composed document for a search query.", http.StatusOK)
		op.Description = "Searches the movies and series the selected profile may see and composes at most 50 results. A deployment without a search provider answers an empty document rather than an error. Served with Cache-Control: private, no-store."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is missing or q is blank.",
			500: "internal_error: the viewer's access could not be resolved, the search failed, or the document could not be composed.",
		})
		registerBloemChiDocument[BloemWatchSearchInput, BloemWatchDocumentOutput](reg, op)
	}

}
