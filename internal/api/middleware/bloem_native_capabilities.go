package middleware

// NativeStorageComposition reports whether the actual finite guards have the
// host credential/session, tenant, viewer/PIN and permission dependencies.
func (m *NativeMutationAccess) NativeStorageComposition() bool {
	return m != nil && m.auth != nil && m.auth.tokenValidator != nil && m.auth.sessionValidator != nil && m.viewer != nil && m.viewer.resolver != nil && m.tenant != nil && m.tenant.resolver != nil && m.users != nil && m.primary != nil && (m.policy != nil || m.legacy != nil)
}
