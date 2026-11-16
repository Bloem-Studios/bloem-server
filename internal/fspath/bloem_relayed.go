package fspath

// RegisteredRelayedSources returns the relay installed by the last
// RegisterRelayedSources, or nil, so a process can wrap it in a relay of its
// own and register that instead. Bloem's API servers do this to redact their
// private tool inputs (internal/nativeredact).
func RegisteredRelayedSources() RelayedSources {
	return relayedSources()
}
