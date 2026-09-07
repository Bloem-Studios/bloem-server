package handlers

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/go-chi/chi/v5"
)

type auxiliaryProducerContextKey struct{}
type auxiliaryProducerContext struct{ session *playback.Session }

// AuxiliaryProducer mounts internal-only producer routes. A signed immutable
// descriptor plus an egress-created permit must acquire API auxiliary authority
// before request-local ownership is installed. No viewer token is synthesized.
func (h *StreamHandler) AuxiliaryProducer(resolve playback.AuxiliaryRecipeResolverV3, acquire playback.ExecutorAuxiliaryGrantProviderV3) http.Handler {
	router := chi.NewRouter()
	handle := func(w http.ResponseWriter, r *http.Request) {
		if h == nil || h.JWTSecret == "" || h.TM == nil || h.fileResolver == nil || resolve == nil || acquire == nil {
			writeNativeAuthorityUnavailable(w)
			return
		}
		sessionID := chi.URLParam(r, "session_id")
		card, claims := verifiedStreamCardFromToken(r.Header.Get("X-Silo-Stream-Token"), sessionID, h.JWTSecret)
		if card == nil || card.Executor == nil || card.Executor.Validate() != nil {
			writeNativeAuthorityUnavailable(w)
			return
		}
		transport := cmp.Or(card.TranscodeTransportID, sessionID)
		resolved, err := resolve(r.Context(), transport, *card.Executor)
		if err != nil || resolved.Card == nil || resolved.RequestedMediaFileID <= 0 {
			writeNativeAuthorityUnavailable(w)
			return
		}
		expected := resolved.Card.ToClaims()
		expected.RegisteredClaims, expected.Version = claims.RegisteredClaims, claims.Version
		if !reflect.DeepEqual(expected, *claims) || resolved.Card.RoutingEgress != string(noderouting.EgressProxy) || resolved.Card.RoutingEgressNodeID <= 0 || resolved.Card.UserID <= 0 || resolved.Card.ProfileID == "" {
			writeNativeAuthorityUnavailable(w)
			return
		}
		permit := r.Header.Get(playback.AuxiliaryTransferHeaderV3)
		provider := func(ctx context.Context, id string, ns playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
			return acquire(ctx, id, ns, permit)
		}
		guarded, request, cleanup, err := playback.GuardExecutorOutputV3(w, r, provider, transport, card.Executor, playback.AttemptGrantAuxiliaryV3)
		if err != nil {
			writeNativeAuthorityUnavailable(w)
			return
		}
		defer cleanup()
		c := resolved.Card
		session := &playback.Session{ID: c.SessionID, UserID: c.UserID, ProfileID: c.ProfileID, MediaFileID: c.MediaFileID, RequestedMediaFileID: resolved.RequestedMediaFileID, Executor: c.Executor, PlayMethod: c.PlayMethod, TranscodeTransportID: transport, RoutingEgress: c.RoutingEgress, RoutingEgressNodeID: c.RoutingEgressNodeID}
		ctx := apimw.SetClaims(request.Context(), &auth.Claims{UserID: c.UserID, ProfileID: c.ProfileID})
		ctx = apimw.SetProfileID(ctx, c.ProfileID)
		request = request.WithContext(context.WithValue(ctx, auxiliaryProducerContextKey{}, &auxiliaryProducerContext{session: session}))
		if strings.HasSuffix(r.URL.Path, "/fonts") {
			response := &auxiliaryFontResponse{ResponseWriter: guarded, output: guarded}
			items, err := h.BoundSubtitleFontBundle(response, request)
			defer response.close()
			if err != nil {
				var status = http.StatusServiceUnavailable
				if api, ok := errors.AsType[*APIError](err); ok {
					status = api.Status
				}
				http.Error(response.output, "Auxiliary producer unavailable", status)
				return
			}
			response.output.Header().Set("Content-Type", "application/json")
			response.output.Header().Set("Cache-Control", "no-store")
			if err := json.NewEncoder(response.output).Encode(items); err != nil {
				panic(http.ErrAbortHandler)
			}
			return
		}
		h.HandleInitialSubtitle(guarded, request)
	}
	const path = "/internal/playback/auxiliary/{session_id}/subtitles/{track}"
	router.Get(path, handle)
	router.Head(path, handle)
	router.Get(path+"/fonts", handle)
	return router
}

// This branch is reachable only after the adapter installed its private
// capability under a validated auxiliary grant. The session is request-local;
// no API serve grant or reconstruct/registration operation occurs.
func (h *StreamHandler) auxiliarySubtitleSession(w http.ResponseWriter, r *http.Request, producer *auxiliaryProducerContext) (http.ResponseWriter, *http.Request, *playback.Session, *models.MediaFile, func(), bool) {
	fileID, err := subtitleSourceFileID(r, producer.session)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return nil, nil, nil, nil, nil, false
	}
	file, err := h.fileResolver.GetByID(r.Context(), fileID)
	if err != nil || file == nil || file.ID != fileID {
		writeError(w, http.StatusNotFound, "not_found", "Media file not found")
		return nil, nil, nil, nil, nil, false
	}
	return w, r, producer.session, file, func() {}, true
}

type auxiliaryFontResponse struct {
	http.ResponseWriter
	output  http.ResponseWriter
	cleanup func()
}

func (w *auxiliaryFontResponse) RetainSubtitleFontResponse(output http.ResponseWriter, _ *http.Request, cleanup func()) {
	w.output, w.cleanup = output, cleanup
}
func (w *auxiliaryFontResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *auxiliaryFontResponse) close() {
	if w.cleanup != nil {
		w.cleanup()
	}
}
