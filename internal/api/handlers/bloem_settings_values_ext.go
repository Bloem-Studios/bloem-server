package handlers

// effectiveSettingValuesResponse names the existing effective-values envelope
// so the client DTO generator can publish its exact wire shape.
type effectiveSettingValuesResponse struct {
	Settings []effectiveSettingValueResponse `json:"settings"`
	Revision int                             `json:"revision"`
}
