package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
	"github.com/google/uuid"
)

// Retained on the existing boot owner before any launch exchange. A staged
// recipe is not permission to replay a launch whose outcome was lost.
type initialPendingSuccessorV3 struct {
	document      playback.RouteReplacementV3
	session       playback.Session
	card          playback.RecipeCard
	opts          playback.TranscodeOpts
	runtime       *playback.TranscodeSession
	predecessor   *playback.TranscodeSession
	launchStarted bool
	ready         *playback.RouteReplacementReadyReceiptV3
}

func initialSuccessorURL(plan *playback.PlanV3, session *playback.Session, card playback.RecipeCard, secret string) error {
	token, err := streamtoken.Sign(card.ToClaims(), secret, playback.MaxTokenTTL)
	if err != nil {
		return err
	}
	if session.RoutingEgressNodeID == 0 {
		plan.Stream.URL = "/api/v1/stream/" + session.ID + "?st=" + url.QueryEscape(token)
		if plan.Delivery == playback.DeliveryTranscodeHLSV3 {
			plan.Stream.URL = "/api/v1/playback/transcode/" + session.ID + "/master.m3u8?st=" + url.QueryEscape(token)
		}
	} else {
		// Preserve the client origin and any configured prefix from the captured
		// predecessor URL. No request Host or worker origin participates.
		origin, err := url.Parse(plan.Stream.URL)
		if err != nil || origin.Scheme == "" || origin.Host == "" {
			return errors.New("captured proxy client origin unavailable")
		}
		marker := "/stream/direct/"
		suffix := token
		if plan.Delivery == playback.DeliveryTranscodeHLSV3 {
			marker = "/stream/transcode/"
			suffix = token + "/master.m3u8"
		}
		escapedPath := origin.EscapedPath()
		index := strings.LastIndex(escapedPath, marker)
		if index < 0 {
			return errors.New("captured proxy stream path unavailable")
		}
		origin.RawPath = escapedPath[:index] + marker + suffix
		origin.Path, err = url.PathUnescape(origin.RawPath)
		if err != nil {
			return err
		}
		origin.RawQuery, origin.Fragment = "", ""
		plan.Stream.URL = origin.String()
	}
	bindInitialSubtitleURLsV3(plan, token)
	return nil
}

func (h *PlaybackHandler) prepareInitialSuccessorV3(ctx context.Context, pending *initialPendingPublicationV3, session *playback.Session, req playback.ReplanRequestV3, digest string, lease playback.ReplanLeaseV3, result playback.PlannerResultV3) (*initialPendingSuccessorV3, error) {
	flow := h.initialFlow
	previous, err := flow.ResolveRecipe(ctx, session.TranscodeTransportID, *session.Executor)
	if err != nil || previous == nil {
		return nil, playbackAuthorityOperationError()
	}
	locator, err := flow.Control.GetAttemptRecipeLocator(ctx, pending.owner.Authority())
	if err != nil || locator == nil {
		return nil, playbackAuthorityOperationError()
	}
	next := *session
	next.Executor = &playback.ExecutorNamespaceV3{Incarnation: session.Executor.Incarnation, Epoch: session.Executor.Epoch, ExecutorID: uuid.NewString()}
	next.TranscodeTransportID = uuid.NewString()
	next.Position = req.PositionSeconds
	planBytes, err := json.Marshal(result.Plan)
	if err != nil {
		return nil, playbackAuthorityOperationError()
	}
	var plan playback.PlanV3
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		return nil, playbackAuthorityOperationError()
	}
	result.Plan = &plan
	plan.SessionID = session.ID
	planID := sha256.Sum256([]byte(pending.record.PlaybackAttemptID + "\x00" + req.ReplanRequestID))
	plan.PlanID = "replacement-" + hex.EncodeToString(planID[:])
	card := *previous
	card.Executor = new(*next.Executor)
	card.TranscodeTransportID = next.TranscodeTransportID
	var opts playback.TranscodeOpts
	if plan.Delivery == playback.DeliveryTranscodeHLSV3 {
		if h.fileResolver == nil {
			return nil, playbackAuthorityOperationError()
		}
		file, fileErr := h.fileResolver.GetByID(ctx, session.MediaFileID)
		if fileErr != nil || file == nil || file.FilePath != previous.InputPath {
			return nil, playbackAuthorityOperationError()
		}
		if err := h.validateFrozenSubtitleIdentityV3(ctx, file, pending.record.FrozenRecipe); err != nil {
			return nil, playbackAuthorityOperationError()
		}
		if err := preflightPlaybackFile(ctx, file, h.MissingMarker, h.EventsHub); err != nil {
			return nil, playbackAuthorityOperationError()
		}
		card, opts, err = h.prepareInitialTranscodeV3(ctx, &next, file, result)
		if err != nil {
			return nil, playbackAuthorityOperationError()
		}
	} else if plan.Delivery != playback.DeliveryOriginalHTTPV3 {
		return nil, playbackOperationError(http.StatusNotImplemented, "capability_unsupported", "Selected successor transport is unavailable")
	}
	if err := initialSuccessorURL(&plan, &next, card, h.JWTSecret); err != nil {
		return nil, playbackAuthorityOperationError()
	}
	nextLocator, err := flow.Recipes.PutImmutable(ctx, card)
	if err != nil {
		return nil, playbackAuthorityOperationError()
	}
	updated := pending.record
	updated.CurrentPlan = plan
	updated.CurrentPlanID = plan.PlanID
	updated.FrozenRecipe.PlanID = plan.PlanID
	updated.CurrentReplanRequestID = req.ReplanRequestID
	updated.NormalizedRequest.StartPosition = new(req.PositionSeconds)
	response := updated.StartResponse
	response.PlaybackPlan = &plan
	updated.StartResponse = response
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, playbackAuthorityOperationError()
	}
	route := playback.AttemptGrantRouteV3{Executor: *next.Executor, TransportID: next.TranscodeTransportID, ExecutionNodeID: next.RoutingExecutionNodeID, EgressNodeID: next.RoutingEgressNodeID}
	previousRoute := playback.AttemptGrantRouteV3{Executor: *session.Executor, TransportID: session.TranscodeTransportID, ExecutionNodeID: session.RoutingExecutionNodeID, EgressNodeID: session.RoutingEgressNodeID}
	document := playback.RouteReplacementV3{Key: playback.RouteReplacementKeyV3{RequestID: req.ReplanRequestID, Digest: digest, LeaseToken: lease.LeaseToken, BaseReplanID: pending.record.CurrentReplanRequestID}, PreviousPlanID: pending.record.CurrentPlanID, PreviousRoute: previousRoute, PreviousLocator: *locator, Next: updated, Route: route, Locator: nextLocator, Response: encoded}
	return &initialPendingSuccessorV3{document: document, session: next, card: card, opts: opts, predecessor: h.tm.GetTranscodeSession(session.ID)}, nil
}

func (h *PlaybackHandler) resumeInitialSuccessorV3(ctx context.Context, pending *initialPendingPublicationV3, successor *initialPendingSuccessorV3) (playback.DecisionResponseV3, error) {
	fail := func() (playback.DecisionResponseV3, error) {
		return playback.DecisionResponseV3{}, playbackAuthorityOperationError()
	}
	store, ok := h.initialFlow.Control.(playback.BoundRouteReplacementStoreV3)
	if !ok {
		return fail()
	}
	doc := successor.document
	if doc.Phase == "" {
		staged, err := store.StageBoundRouteReplacement(ctx, pending.binding, doc)
		if err != nil {
			return fail()
		}
		successor.document = staged
		doc = staged
	} else {
		observed, err := store.ReadBoundRouteReplacement(ctx, pending.binding, doc.Key)
		if err != nil {
			return fail()
		}
		successor.document = observed
		doc = observed
	}
	if doc.Phase == playback.RouteReplacementCancelledV3 {
		h.releaseCancelledInitialSuccessorV3(ctx, pending, successor)
		return fail()
	}
	if doc.Phase == playback.RouteReplacementStagedV3 {
		if !successor.launchStarted {
			successor.launchStarted = true
			var launchErr error
			if successor.card.PlayMethod != playback.PlayDirect {
				if successor.session.TranscodeNodeURL != "" {
					request, err := transcodenode.BoundTranscodeStartRequest(successor.card)
					if err != nil {
						return fail()
					}
					_, status, err := h.startRemotePlaybackTransport(ctx, successor.session.TranscodeNodeURL, request)
					launchErr = err
					if status != http.StatusAccepted && launchErr == nil {
						launchErr = errors.New("candidate worker readiness unavailable")
					}
				} else {
					successor.opts.ExecuteGrants = h.initialFlow.AcquireGrant
					var failure *localTransportStartupFailureV3
					successor.runtime, failure = h.startReadyLocalPlaybackTransportV3(ctx, successor.opts)
					if failure != nil {
						launchErr = failure.cause
					}
				}
			}
			if launchErr != nil {
				// Revocation is a retained candidate cancellation. It never invokes a
				// legacy DELETE or assumes an uncertain start did not execute.
				cleanup, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				_, _ = store.CancelBoundRouteReplacement(cleanup, pending.binding, doc.Key)
				cancelCleanup()
				return fail()
			}
			encoded, err := json.Marshal(successor.card)
			if err != nil {
				return fail()
			}
			digest := sha256.Sum256(encoded)
			successor.ready = &playback.RouteReplacementReadyReceiptV3{Route: doc.Route, Locator: doc.Locator, ReceiptID: hex.EncodeToString(digest[:])}
		}
		if successor.ready == nil {
			cleanup, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancelCleanup()
			_, _ = store.CancelBoundRouteReplacement(cleanup, pending.binding, doc.Key)
			return fail()
		}
		ready, err := store.AcknowledgeBoundRouteReplacement(ctx, pending.binding, doc.Key, *successor.ready)
		if err != nil {
			return fail()
		}
		successor.document = ready
		doc = ready
	}
	if doc.Phase == playback.RouteReplacementReadyV3 {
		retiring, err := store.BeginBoundRouteRetirement(ctx, pending.binding, doc.Key)
		if err != nil {
			return fail()
		}
		successor.document = retiring
		doc = retiring
	}
	if doc.Phase == playback.RouteReplacementRetiringV3 {
		drainCtx, cancelDrain := context.WithTimeout(ctx, 5*time.Second)
		defer cancelDrain()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if pending.owner.Check() != nil {
				return fail()
			}
			committed, err := store.CompleteBoundRouteReplacement(drainCtx, pending.binding, doc.Key)
			if err == nil {
				successor.document = committed
				doc = committed
				break
			}
			// Completion samples database time and is the sole drain-barrier judge.
			// Neither the local wall clock nor a successful preparation grants serve.
			select {
			case <-drainCtx.Done():
				return fail()
			case <-ticker.C:
			}
		}
	}
	if doc.Phase != playback.RouteReplacementCommittedV3 {
		return fail()
	}
	manager, ok := h.sessionMgr.(interface {
		PublishBoundSessionReplacement(context.Context, playback.InitialActivationBindingV3, playback.ExecutorNamespaceV3, string, playback.Session) (*playback.Session, error)
	})
	if !ok {
		return fail()
	}
	projected, err := manager.PublishBoundSessionReplacement(ctx, pending.binding, doc.PreviousRoute.Executor, doc.PreviousRoute.TransportID, successor.session)
	if err != nil {
		return fail()
	}
	if successor.runtime != nil {
		if h.tm.GetTranscodeSession(projected.ID) != successor.runtime && !h.tm.SwapTranscodeSessionIf(projected.ID, successor.predecessor, successor.runtime) {
			return fail()
		}
	}
	if successor.predecessor != nil {
		_ = successor.predecessor.Close()
	}
	pending.record = doc.Next
	pending.session = *projected
	pending.successor = nil
	return doc.Next.StartResponse, nil
}

// The caller holds pending.mu. Confirmed cancellation alone revokes future
// candidate grants; the database must also confirm the retained drain barrier
// before a new explicit intent can replace this blocker.
func (h *PlaybackHandler) releaseCancelledInitialSuccessorV3(ctx context.Context, pending *initialPendingPublicationV3, successor *initialPendingSuccessorV3) bool {
	store, ok := h.initialFlow.Control.(playback.BoundRouteReplacementStoreV3)
	if !ok || pending.successor != successor {
		return false
	}
	doc, err := store.ConfirmBoundRouteCancellation(ctx, pending.binding, successor.document.Key)
	if err != nil {
		return false
	}
	if successor.runtime != nil {
		if err := successor.runtime.Close(); err != nil {
			return false
		}
	}
	if pending.cancelledSuccessors == nil {
		pending.cancelledSuccessors = make(map[string]playback.RouteReplacementKeyV3)
	}
	pending.cancelledSuccessors[doc.Key.RequestID] = doc.Key
	pending.successor = nil
	return true
}
