package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type unavailableProfileStoreProvider struct{ calls int }

func (p *unavailableProfileStoreProvider) ForUser(context.Context, int) (userstore.UserStore, error) {
	p.calls++
	return nil, errors.New("mutable household state unavailable")
}

func (*unavailableProfileStoreProvider) Close() error { return nil }

type receiptOnlyProfileCoordinator struct {
	result lifecycleidempotency.Result
	err    error
}

func (c receiptOnlyProfileCoordinator) Execute(context.Context, lifecycleidempotency.Request, lifecycleidempotency.Mutator) (lifecycleidempotency.Result, error) {
	return c.result, c.err
}

func (c receiptOnlyProfileCoordinator) ExecuteCreate(context.Context, lifecycleidempotency.Request, lifecycleidempotency.CreateMutator) (lifecycleidempotency.Result, error) {
	return c.result, c.err
}

func TestProfileLifecycleReceiptResolvesBeforeMutablePreflights(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		invoke     func(*ProfileHandler, http.ResponseWriter, *http.Request)
		status     int
		storedBody string
	}{
		{name: "create", method: http.MethodPost, path: "/profiles", body: `{"name":"Child"}`, invoke: (*ProfileHandler).HandleCreateProfile, status: http.StatusCreated, storedBody: `{"id":"stored-create"}`},
		{name: "update", method: http.MethodPut, path: "/profiles/profile-1", body: `{"name":"Renamed"}`, invoke: (*ProfileHandler).HandleUpdateProfile, status: http.StatusOK, storedBody: `{"id":"stored-update"}`},
		{name: "delete", method: http.MethodDelete, path: "/profiles/profile-1", invoke: (*ProfileHandler).HandleDeleteProfile, status: http.StatusNoContent},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &unavailableProfileStoreProvider{}
			handler := NewProfileHandler(provider)
			handler.SetLifecycleIdempotency(receiptOnlyProfileCoordinator{result: lifecycleidempotency.Result{
				Status: test.status, Body: []byte(test.storedBody), Replayed: true,
			}}, func(string, string, map[string]string, url.Values, []byte) lifecycleidempotency.Digest {
				return lifecycleidempotency.Digest{1}
			})
			req := profileLifecycleOrderRequest(test.method, test.path, test.body)
			rec := httptest.NewRecorder()

			test.invoke(handler, rec, req)

			if rec.Code != test.status || strings.TrimSpace(rec.Body.String()) != test.storedBody {
				t.Fatalf("response = %d %q, want %d %q", rec.Code, strings.TrimSpace(rec.Body.String()), test.status, test.storedBody)
			}
			if provider.calls != 0 {
				t.Fatalf("mutable store acquired %d times during completed replay", provider.calls)
			}
		})
	}
}

func TestProfileLifecyclePendingResolvesBeforeMutablePreflights(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		invoke func(*ProfileHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "create", method: http.MethodPost, path: "/profiles", body: `{"name":"Child"}`, invoke: (*ProfileHandler).HandleCreateProfile},
		{name: "update", method: http.MethodPut, path: "/profiles/profile-1", body: `{"name":"Renamed"}`, invoke: (*ProfileHandler).HandleUpdateProfile},
		{name: "delete", method: http.MethodDelete, path: "/profiles/profile-1", invoke: (*ProfileHandler).HandleDeleteProfile},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &unavailableProfileStoreProvider{}
			handler := NewProfileHandler(provider)
			handler.SetLifecycleIdempotency(receiptOnlyProfileCoordinator{err: lifecycleidempotency.ErrPending}, func(string, string, map[string]string, url.Values, []byte) lifecycleidempotency.Digest {
				return lifecycleidempotency.Digest{1}
			})
			req := profileLifecycleOrderRequest(test.method, test.path, test.body)
			rec := httptest.NewRecorder()

			test.invoke(handler, rec, req)

			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error":"lifecycle_request_pending"`) {
				t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
			}
			if provider.calls != 0 {
				t.Fatalf("mutable store acquired %d times while receipt pending", provider.calls)
			}
		})
	}
}

func profileLifecycleOrderRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Idempotency-Key", "profile-receipt-order-0001")
	ctx := apimw.SetClaims(req.Context(), &auth.Claims{
		UserID: 1, Role: "admin", TokenType: auth.TokenTypeAccess,
		AccountIncarnationID: uuid.MustParse("10000000-0000-4000-8000-000000000001").String(),
	})
	req = req.WithContext(ctx)
	if strings.Contains(path, "profile-1") {
		req = withProfileRouteParam(req, "id", "profile-1")
	}
	return req
}
