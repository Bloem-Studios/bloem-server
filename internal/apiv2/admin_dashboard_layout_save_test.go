package apiv2

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeDashboardLayoutSave struct {
	users []int
	docs  []json.RawMessage
	err   error
}

func (f *fakeDashboardLayoutSave) SaveAdminDashboardLayout(_ context.Context, id int, doc json.RawMessage) error {
	f.users = append(f.users, id)
	f.docs = append(f.docs, append(json.RawMessage(nil), doc...))
	return f.err
}
func TestAdminDashboardLayoutSave(t *testing.T) {
	f := new(fakeDashboardLayoutSave)
	deps := pilotDeps(nil, nil)
	deps.AdminDashboardLayoutSaves = f
	h := NewHandler(deps)
	path := Prefix + "/admin/dashboard/layout"
	body := `{"layout":{"version":1,"future":{"large":9007199254740993},"entries":[]}}`
	requireProblem(t, do(t, h, "PUT", path, body, nil), TypeAuthenticationRequired)
	requireProblem(t, do(t, h, "PUT", path, body, bearer(memberToken)), TypePermissionDenied)
	for _, bad := range []string{`{}`, `{"layout":null}`, `{"layout":[]}`} {
		requireProblem(t, do(t, h, "PUT", path, bad, bearer(adminToken)), TypeValidationFailed)
	}
	if len(f.docs) != 0 {
		t.Fatal("invalid request dispatched")
	}
	rec := do(t, h, "PUT", path, body, bearer(adminToken))
	if rec.Code != 204 || rec.Body.Len() != 0 || len(f.docs) != 1 || f.users[0] <= 0 || !strings.Contains(string(f.docs[0]), "9007199254740993") {
		t.Fatal(rec.Code, rec.Body.String(), f.docs)
	}
	rec = do(t, h, "PUT", path, `{"layout":{"x":"`+strings.Repeat("x", 17000)+`"}}`, bearer(adminToken))
	if rec.Code != 413 || len(f.docs) != 1 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	f.err = errors.New("private-store")
	rec = do(t, h, "PUT", path, body, bearer(adminToken))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "private-store") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	deps.AdminDashboardLayoutSaves = nil
	requireProblem(t, do(t, NewHandler(deps), "PUT", path, body, bearer(adminToken)), TypeDependencyUnavailable)
}
