package auth

// Bloem-owned. An audience ticket stands in for the credential that minted it
// for one websocket handshake. Rebuilding the handshake principal from a
// handful of fields dropped the rest of that credential: the auth method
// (a direct-profile session became an unbound account principal), the tenant
// binding, the device binding, the impersonator, and an API key's identity
// and scopes. Capturing and restoring the whole principal here keeps every
// mint site and the consume site in step.

// NewAudienceTicket captures claims as the principal of a ticket for audience.
// profileID must be the profile the minting request verified, never one read
// from a resource: the handshake that spends the ticket cannot present a PIN,
// so the ticket carries the verification forward.
func NewAudienceTicket(audience Audience, claims *Claims, profileID, resourceID string) AudienceTicket {
	ticket := AudienceTicket{Audience: audience, ProfileID: profileID, ResourceID: resourceID}
	if claims == nil {
		return ticket
	}
	ticket.AccountID = claims.UserID
	ticket.Role = claims.Role
	ticket.SessionID = claims.SessionID
	ticket.TokenType = claims.TokenType
	ticket.AuthMethod = claims.AuthMethod
	ticket.AccountIncarnationID = claims.AccountIncarnationID
	ticket.DeviceID = claims.DeviceID
	if claims.ImpersonatorUserID != nil {
		impersonator := *claims.ImpersonatorUserID
		ticket.ImpersonatorUserID = &impersonator
	}
	ticket.APIKeyID = claims.APIKeyID
	ticket.RateTier = claims.RateTier
	ticket.APIKeyScopes = append([]string(nil), claims.APIKeyScopes...)
	ticket.OrganizationID = claims.OrganizationID
	ticket.MembershipID = claims.MembershipID
	ticket.PolicyRevision = claims.PolicyRevision
	ticket.SecurityRevision = claims.SecurityRevision
	ticket.CredentialRevision = claims.CredentialRevision
	return ticket
}

// Claims rebuilds the minting credential's claims. ProfileID is the verified
// profile the ticket was minted for.
func (t AudienceTicket) Claims() *Claims {
	claims := &Claims{
		UserID:               t.AccountID,
		AccountIncarnationID: t.AccountIncarnationID,
		Role:                 t.Role,
		SessionID:            t.SessionID,
		ProfileID:            t.ProfileID,
		DeviceID:             t.DeviceID,
		TokenType:            t.TokenType,
		APIKeyID:             t.APIKeyID,
		RateTier:             t.RateTier,
		OrganizationID:       t.OrganizationID,
		MembershipID:         t.MembershipID,
		PolicyRevision:       t.PolicyRevision,
		SecurityRevision:     t.SecurityRevision,
		AuthMethod:           t.AuthMethod,
		CredentialRevision:   t.CredentialRevision,
		APIKeyScopes:         append([]string(nil), t.APIKeyScopes...),
	}
	if t.ImpersonatorUserID != nil {
		impersonator := *t.ImpersonatorUserID
		claims.ImpersonatorUserID = &impersonator
	}
	return claims
}
