package apiv2

// Bloem-owned. Bloem's native /api/bloem/v2 routes serve Silo's /api/v2 wire
// shapes directly: an organization's access groups and invitations, and a
// platform account's devices, are the same objects /api/v2 returns. The
// shapes and their request decoding live in this package, which imports
// internal/api/handlers, so this file installs them there instead of the
// handlers importing them.

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/models"
)

func init() { handlers.SetBloemV2Wire(bloemV2Wire{}) }

type bloemV2Wire struct{}

func (bloemV2Wire) AccessGroup(g access.Group) any { return adminAccessGroupOf(&g) }

func (bloemV2Wire) AccessGroupListItem(g access.Group) any {
	return AdminAccessGroupListItem{AdminAccessGroup: adminAccessGroupOf(&g), MemberCount: g.MemberCount}
}

// AccessGroupCreate decodes an AdminAccessGroupBody as createAdminAccessGroup
// does, with the same defaults for omitted fields.
func (bloemV2Wire) AccessGroupCreate(raw []byte) (access.CreateGroupInput, error) {
	update, err := bloemV2GroupInput(raw)
	if err != nil {
		return access.CreateGroupInput{}, err
	}
	if update.Name == nil {
		return access.CreateGroupInput{}, bloemV2Error(NewProblem(TypeValidationFailed, "Access group name is required."))
	}
	out := access.CreateGroupInput{Name: *update.Name, DownloadAllowed: true, DownloadTranscodeAllowed: true, TranscodeAllowed: true, AudioTranscodeAllowed: true, RequestsAllowed: true}
	applyGroupCreate(&out, update)
	return out, nil
}

// AccessGroupUpdate decodes an AdminAccessGroupBody as updateAdminAccessGroup
// does.
func (bloemV2Wire) AccessGroupUpdate(raw []byte) (access.UpdateGroupInput, error) {
	return bloemV2GroupInput(raw)
}

func (bloemV2Wire) Invitation(inv *models.Invitation) any { return invitationOf(inv) }

func (bloemV2Wire) UserDevices(views []handlers.AdminUserDeviceView) any {
	out := make([]AdminUserDevice, 0, len(views))
	for _, view := range views {
		out = append(out, adminUserDeviceOf(view))
	}
	slices.SortFunc(out, compareAdminUserDevices)
	return out
}

func bloemV2GroupInput(raw []byte) (access.UpdateGroupInput, error) {
	var body AdminAccessGroupBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return access.UpdateGroupInput{}, bloemV2Error(NewProblem(TypeMalformedRequest, "Invalid JSON body."))
	}
	update, p := groupInput(body, raw)
	if p != nil {
		return access.UpdateGroupInput{}, bloemV2Error(p)
	}
	return update, nil
}

// bloemV2Error carries a v2 problem's status and identifier in the native
// surface's error body.
func bloemV2Error(p *Problem) error {
	return &handlers.APIError{Status: p.Status, Code: p.Type[strings.LastIndex(p.Type, "/")+1:], Message: p.Detail}
}
