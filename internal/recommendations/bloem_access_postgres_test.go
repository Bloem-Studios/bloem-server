package recommendations

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/access"
)

// bloemFixedGroupPolicy serves one access group to the resolver. Bloem keeps
// account groups on organization memberships, which Silo's untenanted
// GroupStore cannot read.
type bloemFixedGroupPolicy struct{ policy *access.GroupPolicy }

func (f bloemFixedGroupPolicy) GetPolicyForUser(context.Context, int) (*access.GroupPolicy, error) {
	return f.policy, nil
}
