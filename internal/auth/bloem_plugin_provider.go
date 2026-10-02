package auth

// SetMembershipProvisioner assigns default memberships to auto-provisioned
// plugin accounts.
func (p *PluginProvider) SetMembershipProvisioner(provisioner MembershipProvisioner) {
	if p.resolver != nil && p.resolver.accounts != nil {
		p.resolver.accounts.SetMembershipProvisioner(provisioner)
	}
}
