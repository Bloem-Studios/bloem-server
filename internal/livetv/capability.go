package livetv

// CapabilityResponse separates installed support, viewer permission and
// enabled channel availability. It is scoped to the current viewer.
type CapabilityResponse struct {
	Supported                bool `json:"supported"`
	Allowed                  bool `json:"allowed"`
	Available                bool `json:"available"`
	HeartbeatIntervalSeconds int  `json:"heartbeat_interval_seconds"`
}
