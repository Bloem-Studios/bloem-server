package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
)

// Separate ebook methods preserve the existing audiobook API and its retry
// semantics. The shared handler supplies the same verified viewer boundary.
type BloemEbookRequestsService interface {
	CapabilityEbook(context.Context, bookrequests.Viewer) (bookrequests.Capability, error)
	ListEbooks(context.Context, bookrequests.Viewer, int, int) ([]bookrequests.Request, error)
	GetEbook(context.Context, bookrequests.Viewer, string) (bookrequests.Request, error)
	CreateEbook(context.Context, bookrequests.Viewer, bookrequests.EbookCreateInput, string) (bookrequests.Request, bool, error)
	ActEbook(context.Context, bookrequests.Viewer, string, string, bookrequests.ActionInput) (bookrequests.Request, error)
}

type ebookRequestsAdapter struct{ BloemEbookRequestsService }

func (s ebookRequestsAdapter) Capability(ctx context.Context, v bookrequests.Viewer) (bookrequests.Capability, error) {
	return s.CapabilityEbook(ctx, v)
}
func (s ebookRequestsAdapter) List(ctx context.Context, v bookrequests.Viewer, limit, offset int) ([]bookrequests.Request, error) {
	return s.ListEbooks(ctx, v, limit, offset)
}
func (s ebookRequestsAdapter) Get(ctx context.Context, v bookrequests.Viewer, id string) (bookrequests.Request, error) {
	return s.GetEbook(ctx, v, id)
}
func (s ebookRequestsAdapter) Create(ctx context.Context, v bookrequests.Viewer, in bookrequests.CreateInput, key string) (bookrequests.Request, bool, error) {
	return s.CreateEbook(ctx, v, bookrequests.EbookCreateInput{Title: in.Title, Author: in.Author, ISBN: in.ISBN, Note: in.Note}, key)
}
func (s ebookRequestsAdapter) Act(ctx context.Context, v bookrequests.Viewer, id, action string, in bookrequests.ActionInput) (bookrequests.Request, error) {
	return s.ActEbook(ctx, v, id, action, in)
}

func (h *BloemBookRequestsHandler) SetEbookRequests(s BloemEbookRequestsService) {
	if h != nil && s != nil {
		h.ebooks = &BloemBookRequestsHandler{service: ebookRequestsAdapter{s}, profiles: h.profiles, kind: "Ebook"}
	}
}
func (h *BloemBookRequestsHandler) EbookRequests() *BloemBookRequestsHandler {
	if h == nil {
		return nil
	}
	return h.ebooks
}
