package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

// initialMediaRecipeV3 resolves header media only through the live bound session
// and its immutable executor recipe. Missing state never reconstructs or reads
// a legacy proxy grant. Authentication middleware owns bearer/session validation.
func initialMediaRecipeV3(r *http.Request, manager *playback.TranscodeManager, getSession func(string) (*playback.Session, error), sessionID, secret string) (*playback.RecipeCard, *streamtoken.Claims) {
	if r.URL.Query().Has(streamTokenParam) {
		return verifiedStreamCardFromToken(r.URL.Query().Get(streamTokenParam), sessionID, secret)
	}
	principal := apimw.GetClaims(r.Context())
	scheme, bearer, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(bearer) == "" || principal == nil || principal.TokenType != auth.TokenTypeAccess || manager == nil || manager.ResolveExecutorRecipe == nil || getSession == nil {
		return nil, nil
	}
	session, err := getSession(sessionID)
	if err != nil || session == nil || !session.RequireMediaAuthorization || session.Executor == nil || session.UserID != principal.UserID || session.ProfileID == "" || apimw.GetProfileID(r.Context()) != session.ProfileID || r.Header.Get("X-Profile-Id") != session.ProfileID {
		return nil, nil
	}
	card, err := manager.ResolveExecutorRecipe(r.Context(), session.TranscodeTransportID, *session.Executor)
	if err != nil || card == nil || card.SessionID != sessionID || card.UserID != session.UserID || card.ProfileID != session.ProfileID || card.MediaFileID != session.MediaFileID || card.TranscodeTransportID != session.TranscodeTransportID || playback.MatchExecutorNamespace(card.Executor, session.Executor) != nil {
		return nil, nil
	}
	claims := card.ToClaims()
	return card, &claims
}

// projectInitialHeaderMediaV3 changes only client URL projection. The selected
// origin, identity pins and immutable executor remain those of the attempt.
func projectInitialHeaderMediaV3(plan *playback.PlanV3, session *playback.Session, mode mediaAuthModeV3) error {
	if plan == nil || session == nil || plan.SessionID != session.ID || session.ProfileID == "" || !mode.headerAuth {
		return errors.New("captured header media identity required")
	}
	hls := plan.Delivery == playback.DeliveryTranscodeHLSV3 || plan.Delivery == playback.DeliveryRemuxHLSV3
	if session.RoutingEgressNodeID > 0 {
		if !mode.proxyEgress {
			return errors.New("proxy media origin was not negotiated")
		}
		u, err := url.Parse(plan.Stream.URL)
		if err != nil || (u.Scheme != eventsSchemeHTTP && u.Scheme != eventsSchemeHTTPS) || u.Host == "" || u.User != nil {
			return errors.New("captured proxy origin unavailable")
		}
		i := strings.LastIndex(u.EscapedPath(), "/stream/")
		if i < 0 {
			return errors.New("captured proxy path unavailable")
		}
		u.RawPath = u.EscapedPath()[:i] + "/stream/v3/" + url.PathEscape(session.ID)
		if hls {
			u.RawPath += "/master.m3u8"
		}
		u.Path, err = url.PathUnescape(u.RawPath)
		if err != nil {
			return err
		}
		u.RawQuery, u.Fragment = "", ""
		plan.Stream.URL = u.String()
		return bindInitialProxyAuxiliaryURLsV3(plan, session.ProfileID)
	}
	plan.Stream.URL = "/api/v2/stream/" + url.PathEscape(session.ID)
	if hls {
		plan.Stream.URL = "/api/v2/playback/transcode/" + url.PathEscape(session.ID) + "/master.m3u8"
	}
	bind := func(raw string) (string, error) {
		if raw == "" {
			return "", nil
		}
		u, err := url.Parse(raw)
		if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" {
			return "", errors.New("captured API auxiliary origin required")
		}
		path := strings.TrimPrefix(strings.TrimPrefix(u.Path, "/api/v1"), "/api/v2")
		if !strings.HasPrefix(path, "/stream/"+session.ID+"/subtitles/") {
			return "", errors.New("captured auxiliary session differs")
		}
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return "", err
		}
		q.Del(streamTokenParam)
		if q.Get("file_id") == "" {
			return "", errors.New("captured auxiliary file identity required")
		}
		u.Path, u.RawPath, u.RawQuery = "/api/v2"+path, "", q.Encode()
		return u.String(), nil
	}
	var err error
	for i := range plan.Subtitle.Inventory {
		item := &plan.Subtitle.Inventory[i]
		if item.URL, err = bind(item.URL); err != nil {
			return err
		}
		if item.FontBundleURL, err = bind(item.FontBundleURL); err != nil {
			return err
		}
	}
	if plan.Subtitle.Artifact != nil {
		if plan.Subtitle.Artifact.URL, err = bind(plan.Subtitle.Artifact.URL); err != nil {
			return err
		}
	}
	if plan.Stream.Headers == nil {
		plan.Stream.Headers = make(map[string]string)
	}
	plan.Stream.Headers["X-Profile-Id"] = session.ProfileID
	return nil
}
