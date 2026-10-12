package api

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/requests"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
)

// BuildBloemBookRequestsService composes request backends before scheduled tasks start.
// Router construction may compose another stateless service over the same durable DB.
func BuildBloemBookRequestsService(deps Dependencies) *bookrequests.Service {
	if deps.DB == nil {
		return nil
	}
	var notifier bookrequests.Notifier
	if n := notifications.NewBookRequestNotifier(deps.Notifications); n != nil {
		notifier = n
	}
	service := bookrequests.NewService(deps.DB, notifier)
	if deps.PluginService != nil && deps.SecretCipher != nil {
		repository := requests.NewRepository(deps.DB, deps.SecretCipher)
		resolver := PluginRequestRouterAdapter{Svc: deps.PluginService}
		service.SetEbookFulfillment(bloemEbookRouter{integrations: repository, resolver: resolver, validator: service})
		service.SetAudiobookFulfillment(bloemAudiobookRouter{integrations: repository, resolver: resolver, validator: service})
	}
	return service
}

func newBloemBookRequests(deps Dependencies) *handlers.BloemBookRequestsHandler {
	if deps.DB == nil {
		return nil
	}
	service := BuildBloemBookRequestsService(deps)
	var setup handlers.BloemEbookRequestSetup
	var audiobookSetup handlers.BloemAudiobookRequestSetup
	if deps.PluginService != nil && deps.SecretCipher != nil {
		repository := requests.NewRepository(deps.DB, deps.SecretCipher)
		connections := requests.NewService(repository, nil, nil)
		connections.SetTenantScopeResolver(tenancy.NewStore(deps.DB))
		AttachRequestRouter(connections, deps.PluginService)
		platform := deps.PlatformAdminAuthorizer
		if platform == nil {
			platform = auth.NewPlatformAdminAuthorizer(auth.NewUserRepository(deps.DB))
		}
		setup = bloemEbookSetup{pool: deps.DB, books: service, requests: connections, platform: platform}
		audiobookSetup = bloemAudiobookSetup{pool: deps.DB, books: service, requests: connections, platform: platform}
	}
	h := handlers.NewBloemBookRequestsHandler(service, tenancy.NewStore(deps.DB))
	h.SetEbookRequests(service)
	h.SetAudiobookAcquisition(service)
	h.SetAudiobookRequestSetup(audiobookSetup)
	h.SetEbookRequestSetup(setup)
	return h
}
func mountBloemBookRequests(r chi.Router, h *handlers.BloemBookRequestsHandler, client bloemClientSurface, system *handlers.BloemSystemHandler) {
	available := h != nil && client.auth != nil && client.tenant != nil && client.viewer != nil
	if system != nil {
		system.SetBookRequestsAvailable(available)
		system.SetEbookRequestsAvailable(available && h.EbookRequests() != nil)
	}
	if !available {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(client.auth.RequireAuth)
		r.Use(client.tenant.ResolveNative)
		if client.rateLimit != nil {
			r.Use(client.rateLimit.Handler)
		}
		r.Use(client.viewer.RequireViewerAccess)
		r.Use(apimw.RequireProfile)
		r.Get("/book-requests/capability", h.HandleCapability)
		r.Get("/book-requests/mine", h.HandleMine)
		r.Post("/book-requests", h.HandleCreate)
		r.Get("/book-requests/{id}", h.HandleGet)
		r.Post("/book-requests/{id}/cancel", h.HandleCancel)
		if ebooks := h.EbookRequests(); ebooks != nil {
			r.Get("/ebook-requests/capability", ebooks.HandleCapability)
			r.Get("/ebook-requests/mine", ebooks.HandleMine)
			r.Post("/ebook-requests", ebooks.HandleCreate)
			r.Get("/ebook-requests/{id}", ebooks.HandleGet)
			r.Post("/ebook-requests/{id}/cancel", ebooks.HandleCancel)
		}
	})
}
func mountBloemAdminBookRequests(r chi.Router, h *handlers.BloemBookRequestsHandler) {
	if h == nil {
		return
	}
	r.Get("/organization/book-requests", h.HandleAdminList)
	r.Get("/organization/book-requests/acquisitions", h.HandleAudiobookAcquisitionList)
	r.Get("/organization/book-requests/{id}/acquisition", h.HandleAudiobookAcquisition)
	r.Post("/organization/book-requests/{id}/approve-candidate", h.HandleAudiobookCandidateApproval)
	r.Post("/organization/book-requests/{id}/accept-download", h.HandleAudiobookDownloadAcceptance)
	r.Post("/organization/book-requests/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		chi.RouteContext(r.Context()).URLParams.Add("action", "retry")
		h.HandleAdminAction(w, r)
	})
	r.Get("/organization/audiobook-request-integrations", h.HandleAudiobookIntegrationList)
	r.Post("/organization/audiobook-request-integrations", h.HandleAudiobookIntegrationCreate)
	r.Put("/organization/audiobook-request-integrations/{id}/library", h.HandleAudiobookIntegrationLibrary)

	// Each route stays literal for contract and authority inventories.
	r.Post("/organization/book-requests/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		chi.RouteContext(r.Context()).URLParams.Add("action", "approve")
		h.HandleAdminAction(w, r)
	})
	r.Post("/organization/book-requests/{id}/decline", func(w http.ResponseWriter, r *http.Request) {
		chi.RouteContext(r.Context()).URLParams.Add("action", "decline")
		h.HandleAdminAction(w, r)
	})
	r.Post("/organization/book-requests/{id}/fulfil", func(w http.ResponseWriter, r *http.Request) { //nolint:misspell // Preserve the established route spelling.
		chi.RouteContext(r.Context()).URLParams.Add("action", "fulfil") //nolint:misspell // Match the established action identifier.
		h.HandleAdminAction(w, r)
	})
	if ebooks := h.EbookRequests(); ebooks != nil {
		r.Get("/organization/ebook-requests", ebooks.HandleAdminList)
		r.Get("/organization/ebook-request-integrations", ebooks.HandleEbookIntegrationList)
		r.Post("/organization/ebook-request-integrations", ebooks.HandleEbookIntegrationCreate)
		r.Put("/organization/ebook-request-integrations/{id}/library", ebooks.HandleEbookIntegrationLibrary)
		r.Put("/organization/ebook-request-policy", ebooks.HandleEbookRequestPolicy)
		r.Post("/organization/ebook-requests/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
			chi.RouteContext(r.Context()).URLParams.Add("action", "approve")
			ebooks.HandleAdminAction(w, r)
		})
		r.Post("/organization/ebook-requests/{id}/decline", func(w http.ResponseWriter, r *http.Request) {
			chi.RouteContext(r.Context()).URLParams.Add("action", "decline")
			ebooks.HandleAdminAction(w, r)
		})
		r.Post("/organization/ebook-requests/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
			chi.RouteContext(r.Context()).URLParams.Add("action", "retry")
			ebooks.HandleAdminAction(w, r)
		})
	}
}
