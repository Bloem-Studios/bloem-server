package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
)

type authLifecycleReplayCoordinator struct {
	request lifecycleidempotency.Request
	result  lifecycleidempotency.Result
}

func (c *authLifecycleReplayCoordinator) Execute(context.Context, lifecycleidempotency.Request, lifecycleidempotency.Mutator) (lifecycleidempotency.Result, error) {
	panic("unexpected non-create lifecycle execution")
}

func (c *authLifecycleReplayCoordinator) ExecuteCreate(_ context.Context, request lifecycleidempotency.Request, _ lifecycleidempotency.CreateMutator) (lifecycleidempotency.Result, error) {
	c.request = request
	return c.result, nil
}

type authLifecycleIdentity struct{ id string }

func (i authLifecycleIdentity) Resolve(context.Context) (string, error) { return i.id, nil }

func TestPublicAuthLifecycleReplayPrecedesConsumedCreateSources(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		body    string
		routeID string
		invoke  func(*AuthHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "setup", path: "/api/v1/auth/setup", body: `{"username":"owner","email":"owner@example.test","password":"secret"}`, routeID: "auth.setup", invoke: (*AuthHandler).HandleSetup},
		{name: "signup", path: "/api/v1/auth/signup", body: `{"username":"member","email":"member@example.test","password":"secret","invite_code":"consumed-code"}`, routeID: "auth.signup", invoke: (*AuthHandler).HandleSignup},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stored := []byte(`{"access_token":"stored-token"}`)
			coordinator := &authLifecycleReplayCoordinator{result: lifecycleidempotency.Result{Status: http.StatusCreated, Body: stored, Headers: map[string][]string{"Content-Type": {"application/json"}}, Replayed: true}}
			handler := NewAuthHandler(nil, nil, nil)
			handler.SetLifecycleIdempotency(coordinator, func(string, string, map[string]string, url.Values, []byte) lifecycleidempotency.Digest {
				return lifecycleidempotency.Digest{1}
			}, lifecycleidempotency.NewPreauthActorDigester([]byte("test-secret")), authLifecycleIdentity{id: "server-one"})
			req := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			req.Header.Set("Idempotency-Key", "public-create-replay-0001")
			rec := httptest.NewRecorder()

			test.invoke(handler, rec, req)

			if rec.Code != http.StatusCreated || strings.TrimSpace(rec.Body.String()) != string(stored) {
				t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
			}
			if coordinator.request.Binding.ActorKind != lifecycleidempotency.ActorPreauthIntent || coordinator.request.Binding.RouteID != test.routeID {
				t.Fatalf("binding = %+v", coordinator.request.Binding)
			}
		})
	}
}
