package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type accountPolicyReaderStub struct {
	snapshot       entitlements.AccountPolicySnapshot
	items          []entitlements.AccountPolicySnapshotResult
	observedAt     time.Time
	err            error
	organizationID uuid.UUID
	accountID      int
	accountIDs     []int
	singleCalls    int
	bulkCalls      int
}

func (s *accountPolicyReaderStub) GetAccountPolicy(_ context.Context, organizationID uuid.UUID, accountID int) (entitlements.AccountPolicySnapshot, error) {
	s.organizationID = organizationID
	s.accountID = accountID
	s.singleCalls++
	return s.snapshot, s.err
}

func (s *accountPolicyReaderStub) GetAccountPolicies(_ context.Context, organizationID uuid.UUID, accountIDs []int) ([]entitlements.AccountPolicySnapshotResult, time.Time, error) {
	s.organizationID = organizationID
	s.accountIDs = append([]int{}, accountIDs...)
	s.bulkCalls++
	return s.items, s.observedAt, s.err
}

func accountPolicyRouter(handler *AdminHandler, claims auth.AdminContextClaims) http.Handler {
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.SetAdminContextClaims(r.Context(), claims)))
		})
	})
	router.Get("/accounts/{account_id}/entitlement", handler.HandleGetAccountPolicy)
	router.Get("/organizations/{organization_id}/accounts/{account_id}/entitlement", handler.HandleGetOrganizationAccountPolicy)
	router.Post("/accounts/entitlement-snapshots", handler.HandleGetAccountPolicySnapshots)
	router.Post("/organizations/{organization_id}/entitlement-snapshots", handler.HandleGetOrganizationAccountPolicySnapshots)
	return router
}

func TestAccountPolicyReadRequiresPlatformAuthority(t *testing.T) {
	store := &accountPolicyReaderStub{}
	handler := &AdminHandler{}
	handler.SetAccountPolicies(store)
	recorder := httptest.NewRecorder()
	accountPolicyRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopeOrganization, AccountID: 7}).ServeHTTP(
		recorder, httptest.NewRequest(http.MethodGet, "/accounts/42/entitlement", nil),
	)
	accountPolicyRequireEqual(t, http.StatusForbidden, recorder.Code)
	accountPolicyRequireContains(t, recorder.Body.String(), `"error":"insufficient_platform_authority"`)
}

func TestAccountPolicyReadUsesOnlyAuthoritativePathScope(t *testing.T) {
	organizationID := uuid.New()
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	store := &accountPolicyReaderStub{snapshot: entitlements.AccountPolicySnapshot{
		ObservedAt: observedAt, OrganizationID: organizationID, AccountID: 42,
		GroupID: 9, State: entitlements.AccountPolicyStateManaged,
		Policy: entitlements.EffectivePolicySnapshot{
			LibraryIDs: []int{3, 5}, PlaybackAllowed: true, AudioTranscodeAllowed: true,
		},
	}}
	handler := &AdminHandler{}
	handler.SetAccountPolicies(store)
	router := accountPolicyRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/organizations/"+organizationID.String()+"/accounts/42/entitlement", nil))
	accountPolicyRequireEqual(t, http.StatusOK, recorder.Code, recorder.Body.String())
	accountPolicyRequireEqual(t, organizationID, store.organizationID)
	accountPolicyRequireEqual(t, 42, store.accountID)
	var payload map[string]any
	accountPolicyRequireNoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	if _, ok := payload["email"]; ok {
		t.Fatalf("payload unexpectedly contains email: %v", payload)
	}
	if _, ok := payload["expected_template_key"]; ok {
		t.Fatalf("payload unexpectedly contains expected_template_key: %v", payload)
	}
	accountPolicyRequireEqual(t, float64(42), payload["account_id"])
	policy, ok := payload["policy"].(map[string]any)
	if !ok {
		t.Fatalf("policy = %#v, want JSON object", payload["policy"])
	}
	accountPolicyRequireEqual(t, []any{float64(3), float64(5)}, policy["library_ids"])
	accountPolicyRequireEqual(t, true, policy["audio_transcode_allowed"])
	if _, ok := policy["LibraryIDs"]; ok {
		t.Fatalf("policy unexpectedly exposes Go field name: %v", policy)
	}
}

func TestDirectAccountPolicyReadResolvesDefaultScopeInStore(t *testing.T) {
	store := &accountPolicyReaderStub{snapshot: entitlements.AccountPolicySnapshot{AccountID: 42}}
	handler := &AdminHandler{}
	handler.SetAccountPolicies(store)
	recorder := httptest.NewRecorder()
	accountPolicyRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}).ServeHTTP(
		recorder, httptest.NewRequest(http.MethodGet, "/accounts/42/entitlement", nil),
	)
	accountPolicyRequireEqual(t, http.StatusOK, recorder.Code, recorder.Body.String())
	accountPolicyRequireEqual(t, uuid.Nil, store.organizationID)
	accountPolicyRequireEqual(t, 42, store.accountID)
}

func TestAccountPolicyReadCollapsesCrossOrganizationAccountToNotFound(t *testing.T) {
	store := &accountPolicyReaderStub{err: entitlements.ErrAccountNotFound}
	handler := &AdminHandler{}
	handler.SetAccountPolicies(store)
	recorder := httptest.NewRecorder()
	accountPolicyRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}).ServeHTTP(
		recorder, httptest.NewRequest(http.MethodGet, "/organizations/"+uuid.NewString()+"/accounts/42/entitlement", nil),
	)
	accountPolicyRequireEqual(t, http.StatusNotFound, recorder.Code)
	accountPolicyRequireContains(t, recorder.Body.String(), `"error":"not_found"`)
}

func TestOrganizationAccountPolicyRoutesRejectNilOrganizationBeforeStore(t *testing.T) {
	nilOrganizationPath := uuid.Nil.String()
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "single", method: http.MethodGet, path: "/organizations/" + nilOrganizationPath + "/accounts/42/entitlement"},
		{name: "bulk", method: http.MethodPost, path: "/organizations/" + nilOrganizationPath + "/entitlement-snapshots", body: `{"account_ids":[42]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &accountPolicyReaderStub{}
			handler := &AdminHandler{}
			handler.SetAccountPolicies(store)
			recorder := httptest.NewRecorder()
			accountPolicyRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}).ServeHTTP(
				recorder, httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)),
			)
			accountPolicyRequireEqual(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
			accountPolicyRequireEqual(t, 0, store.singleCalls)
			accountPolicyRequireEqual(t, 0, store.bulkCalls)
		})
	}
}

func TestEntitlementSnapshotBulkReturnsOneObservationAndSafeItems(t *testing.T) {
	observedAt := time.Date(2026, 8, 22, 13, 0, 0, 0, time.UTC)
	snapshot := &entitlements.AccountPolicySnapshot{ObservedAt: observedAt, AccountID: 41, State: entitlements.AccountPolicyStateCustom}
	store := &accountPolicyReaderStub{
		observedAt: observedAt,
		items: []entitlements.AccountPolicySnapshotResult{
			{AccountID: 41, Snapshot: snapshot},
			{AccountID: 999, Error: entitlements.AccountPolicyResultNotFound},
		},
	}
	handler := &AdminHandler{}
	handler.SetAccountPolicies(store)
	recorder := httptest.NewRecorder()
	accountPolicyRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}).ServeHTTP(
		recorder, httptest.NewRequest(http.MethodPost, "/accounts/entitlement-snapshots", strings.NewReader(`{"account_ids":[41,999]}`)),
	)
	accountPolicyRequireEqual(t, http.StatusOK, recorder.Code, recorder.Body.String())
	accountPolicyRequireEqual(t, []int{41, 999}, store.accountIDs)
	accountPolicyRequireEqual(t, uuid.Nil, store.organizationID)
	var payload struct {
		ObservedAt time.Time                                  `json:"observed_at"`
		Items      []entitlements.AccountPolicySnapshotResult `json:"items"`
	}
	accountPolicyRequireNoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	accountPolicyRequireEqual(t, observedAt, payload.ObservedAt)
	accountPolicyRequireEqual(t, 2, len(payload.Items))
	accountPolicyRequireEqual(t, entitlements.AccountPolicyResultNotFound, payload.Items[1].Error)
}

func TestEntitlementSnapshotRejectsCallerExpectedTemplateAssertion(t *testing.T) {
	store := &accountPolicyReaderStub{}
	handler := &AdminHandler{}
	handler.SetAccountPolicies(store)
	recorder := httptest.NewRecorder()
	accountPolicyRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}).ServeHTTP(
		recorder, httptest.NewRequest(http.MethodPost, "/accounts/entitlement-snapshots", strings.NewReader(`{"account_ids":[41],"expected_template_key":"premium"}`)),
	)
	accountPolicyRequireEqual(t, http.StatusBadRequest, recorder.Code)
	accountPolicyRequireEqual(t, 0, store.bulkCalls)
}

func TestEntitlementSnapshotRejectsTenThousandAndOneIDsBeforeStore(t *testing.T) {
	store := &accountPolicyReaderStub{}
	handler := &AdminHandler{}
	handler.SetAccountPolicies(store)
	var body strings.Builder
	body.WriteString(`{"account_ids":[`)
	for id := 1; id <= entitlements.MaxAccountPolicySnapshotIDs+1; id++ {
		if id > 1 {
			body.WriteByte(',')
		}
		body.WriteString(strconv.Itoa(id))
	}
	body.WriteString(`]}`)
	recorder := httptest.NewRecorder()
	accountPolicyRouter(handler, auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}).ServeHTTP(
		recorder, httptest.NewRequest(http.MethodPost, "/accounts/entitlement-snapshots", strings.NewReader(body.String())),
	)
	accountPolicyRequireEqual(t, http.StatusUnprocessableEntity, recorder.Code, recorder.Body.String())
	accountPolicyRequireContains(t, recorder.Body.String(), `"account_ids"`)
	accountPolicyRequireEqual(t, 0, store.bulkCalls)
}

func accountPolicyRequireEqual(t *testing.T, want, got any, message ...any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("got = %#v, want %#v %v", got, want, message)
	}
}

func accountPolicyRequireContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("%q does not contain %q", got, want)
	}
}

func accountPolicyRequireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
