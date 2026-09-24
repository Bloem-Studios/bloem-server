package apiv2

type ProfileDeleteInput struct {
	ID             ID     `path:"id" doc:"The profile" example:"1"`
	IdempotencyKey string `header:"Idempotency-Key" doc:"Opaque lifecycle retry key"`
}
