package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/go-chi/chi/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
)

// Bloem-owned. Request helpers Silo deleted with its alpha v1 API tests
// (Silo #3200) that Bloem's own tests still build requests with.

func newAuthorizedProfileRequestWithRole(
	method, path, body, role, activeProfileID string,
) *http.Request {
	return newAuthorizedProfileRequestWithSession(method, path, body, role, activeProfileID, "")
}

func newAuthorizedProfileRequestWithSession(
	method, path, body, role, activeProfileID, sessionID string,
) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := apimw.SetClaims(context.Background(), &auth.Claims{
		UserID:    1,
		Role:      role,
		SessionID: sessionID,
		TokenType: auth.TokenTypeAccess,
	})
	if activeProfileID != "" {
		ctx = apimw.SetProfileID(ctx, activeProfileID)
	}
	return req.WithContext(ctx)
}

func withProfileRouteParam(req *http.Request, key, value string) *http.Request {
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add(key, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

func newEbookReaderAuthRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	ctx := apimw.SetClaims(context.Background(), &auth.Claims{
		UserID:    1,
		Role:      "user",
		TokenType: auth.TokenTypeAccess,
	})
	ctx = apimw.SetProfileID(ctx, "profile-1")
	return req.WithContext(ctx)
}

func withEbookReaderRouteParams(req *http.Request, contentID, fileID string) *http.Request {
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("content_id", contentID)
	routeCtx.URLParams.Add("file_id", fileID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

func withChiID(r *http.Request, id string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
