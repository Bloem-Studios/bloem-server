package handlers

import (
	"encoding/json"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
)

// This package's tests cannot link internal/apiv2 (it imports this package),
// so they install a stand-in that projects only a few public fields, the way
// the real wire projects only v2's. The handlers' scoping and revision checks
// are what these tests cover; internal/apiv2's
// TestBloemV2WireServesTheV2Shapes covers the real wire.
func init() { SetBloemV2Wire(testBloemV2Wire{}) }

type testBloemV2Wire struct{}

type testV2Group struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type testV2Invitation struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

func (testBloemV2Wire) AccessGroup(g access.Group) any         { return testV2Group{g.ID, g.Name} }
func (testBloemV2Wire) AccessGroupListItem(g access.Group) any { return testV2Group{g.ID, g.Name} }
func (testBloemV2Wire) Invitation(inv *models.Invitation) any {
	return testV2Invitation{inv.ID, inv.Email}
}
func (testBloemV2Wire) UserDevices(v []AdminUserDeviceView) any {
	return v
}

func (testBloemV2Wire) AccessGroupCreate(raw []byte) (access.CreateGroupInput, error) {
	var body struct {
		Name      string `json:"name"`
		IsDefault bool   `json:"is_default"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Name == "" {
		return access.CreateGroupInput{}, apiError(422, "validation_failed", "Access group name is required.")
	}
	return access.CreateGroupInput{Name: body.Name, IsDefault: body.IsDefault}, nil
}

func (testBloemV2Wire) AccessGroupUpdate(raw []byte) (access.UpdateGroupInput, error) {
	var body struct {
		Name      *string `json:"name"`
		IsDefault *bool   `json:"is_default"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return access.UpdateGroupInput{}, apiError(400, "malformed_request", "Invalid JSON body.")
	}
	return access.UpdateGroupInput{Name: body.Name, IsDefault: body.IsDefault}, nil
}
