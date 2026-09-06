package apiv2

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"
)

type AdminDashboardLayoutSaveService interface {
	SaveAdminDashboardLayout(context.Context, int, json.RawMessage) error
}
type DashboardLayoutWriteDocument json.RawMessage

func (v *DashboardLayoutWriteDocument) UnmarshalJSON(b []byte) error {
	*v = append((*v)[:0], b...)
	return nil
}
func (DashboardLayoutWriteDocument) Schema(reg huma.Registry) *huma.Schema {
	return (DashboardLayoutDocument{}).Schema(reg)
}

type AdminDashboardLayoutSaveBody struct {
	Layout DashboardLayoutWriteDocument `json:"layout"`
}
type AdminDashboardLayoutSaveInput struct{ Body AdminDashboardLayoutSaveBody }

func registerAdminDashboardLayoutSave(reg *Registry) {
	op := Operation{Operation: humaOp("PUT", Prefix+"/admin/dashboard/layout", "saveAdminDashboardLayout", "admin-observability", "Store this administrator account's client-owned layout object. Last write wins; no revision receipt, automatic replay or cross-tab ordering guarantee."), Class: ClassActingAdmin, DemoRestricted: true, ServiceBacked: true, RetrySafety: RetrySafetyNonRetryable}
	op.MaxBodyBytes = 16 << 10
	Register(reg, op, func(ctx context.Context, in *AdminDashboardLayoutSaveInput) (*struct{}, error) {
		if reg.deps.AdminDashboardLayoutSaves == nil {
			return nil, unavailable("dashboard layout")
		}
		layout := bytes.TrimSpace(in.Body.Layout)
		if len(layout) == 0 || layout[0] != '{' || !json.Valid(layout) {
			return nil, NewProblem(TypeValidationFailed, "A layout object is required.")
		}
		if err := reg.deps.AdminDashboardLayoutSaves.SaveAdminDashboardLayout(ctx, claimsFrom(ctx).UserID, layout); err != nil {
			return nil, serviceProblem(err)
		}
		return nil, nil
	})
}
