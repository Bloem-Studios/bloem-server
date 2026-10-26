package handlers

import (
	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
)

// discoverSectionsResponse is the GET /requests/discover envelope. It was an
// inline struct literal, which had no nameable type for the client DTO
// registry (contracts/client/v1/registry.json).
type discoverSectionsResponse struct {
	Sections []mediarequests.DiscoverySection `json:"sections"`
}

// discoverStudiosResponse is the GET /requests/discover/studios envelope. It
// was an inline struct literal, which had no nameable type for the client DTO
// registry (contracts/client/v1/registry.json).
type discoverStudiosResponse struct {
	Studios []mediarequests.DiscoverBrandCard `json:"studios"`
}

// discoverNetworksResponse is the GET /requests/discover/networks envelope.
// It was an inline struct literal, which had no nameable type for the client
// DTO registry (contracts/client/v1/registry.json).
type discoverNetworksResponse struct {
	Networks []mediarequests.DiscoverBrandCard `json:"networks"`
}

// discoverGenresResponse is the GET /requests/discover/genres envelope. It
// was an inline struct literal, which had no nameable type for the client DTO
// registry (contracts/client/v1/registry.json).
type discoverGenresResponse struct {
	Genres []mediarequests.DiscoverBrandCard `json:"genres"`
}

// requestListResponse is the GET /requests/mine envelope (the admin list
// endpoint writes the same shape). It was an inline struct literal, which had
// no nameable type for the client DTO registry
// (contracts/client/v1/registry.json).
type requestListResponse struct {
	Requests []*mediarequests.Request `json:"requests"`
}

// requestReasonRequest is the body of POST /requests/{id}/cancel and of the
// admin decline endpoint. Both handlers wrote it as an inline struct literal,
// which had no nameable type for the client DTO registry
// (contracts/client/v1/registry.json).
type requestReasonRequest struct {
	Reason string `json:"reason"`
}

// requestValidationErrorResponse is the 400 body writeRequestServiceError
// writes when request creation fails field validation. It was an inline
// map[string]any literal, which had no nameable type for the client DTO
// registry (contracts/client/v1/registry.json). The map marshaled its keys
// in sorted order; the struct declares the fields in that same order so the
// bytes are unchanged, including the empty form_error the map always wrote.
type requestValidationErrorResponse struct {
	Error       string            `json:"error"`
	FieldErrors map[string]string `json:"field_errors"`
	FormError   string            `json:"form_error"`
}

// requestQuotaErrorResponse is the 429 body writeRequestServiceError writes
// when the viewer has exhausted their request quota. It was an inline struct
// literal, which had no nameable type for the client DTO registry
// (contracts/client/v1/registry.json).
type requestQuotaErrorResponse struct {
	Error      string `json:"error"`
	Message    string `json:"message"`
	Used       int    `json:"used"`
	Limit      int    `json:"limit"`
	WindowDays int    `json:"window_days"`
}
