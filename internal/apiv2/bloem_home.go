package apiv2

// HomeSectionsInput is the listHomeSections query.
type HomeLayoutInput struct {
	Promoted string `query:"promoted" enum:"1" doc:"Opt in to promoted home sections"`
}
