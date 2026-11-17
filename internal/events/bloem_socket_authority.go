package events

// BloemSocketAuthority is Bloem's field on SocketIdentity, embedded there by
// one line so it is promoted (identity.AuthorityBinding) and keeps its JSON
// name in the stored ticket.
type BloemSocketAuthority struct {
	// AuthorityBinding is an optional server-owned adapter binding. It travels
	// with the single-use proof and is never accepted from a client or exposed.
	AuthorityBinding string `json:"authority_binding,omitempty"`
}
