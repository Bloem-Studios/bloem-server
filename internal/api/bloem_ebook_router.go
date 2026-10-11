package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/requests"
	"google.golang.org/protobuf/types/known/structpb"
)

type ebookIntegrationReader interface {
	GetIntegration(context.Context, string) (*requests.Integration, error)
}
type ebookIntegrationValidator interface {
	ValidateEbookIntegration(context.Context, bookrequests.EbookIntegration) error
}

// The ordinary plugin runtime owns connection credentials. The book service
// retains only the selected installation/configuration and a bounded receipt.
type bloemEbookRouter struct {
	integrations ebookIntegrationReader
	resolver     requests.RouterClientResolver
	validator    ebookIntegrationValidator
}

func (a bloemEbookRouter) connection(ctx context.Context, in bookrequests.DispatchInput) (*pluginv1.RouterConnection, string, error) {
	if a.integrations == nil || a.resolver == nil || a.validator == nil || in.Request.MediaType != "book" || in.Request.ISBN == "" || in.Request.ID == "" {
		return nil, "", bookrequests.ErrInvalid
	}
	if err := a.validator.ValidateEbookIntegration(ctx, in.Integration); err != nil {
		return nil, "", err
	}
	connection, err := a.integrations.GetIntegration(ctx, in.Integration.ID)
	if err != nil {
		return nil, "", errors.New("ebook fulfillment connection unavailable")
	}
	if connection == nil || !connection.Enabled || connection.ID != in.Integration.ID || connection.InstallationID == nil || *connection.InstallationID != in.Integration.InstallationID || connection.CapabilityID != in.Integration.CapabilityID || !slices.Contains(connection.SupportedMediaTypes, "book") || strings.TrimSpace(connection.APIKeyRef) == "" {
		return nil, "", bookrequests.ErrConflict
	}
	format := "epub"
	if configured, exists := connection.PluginConfig["preferred_format"]; exists {
		var valid bool
		format, valid = configured.(string)
		if !valid || format != "epub" && (format != "pdf" || connection.PluginConfig["allow_pdf"] != true) {
			return nil, "", bookrequests.ErrInvalid
		}
	}
	config, err := structpb.NewStruct(connection.PluginConfig)
	if err != nil {
		return nil, "", bookrequests.ErrInvalid
	}
	if err = a.validator.ValidateEbookIntegration(ctx, in.Integration); err != nil {
		return nil, "", err
	}
	return &pluginv1.RouterConnection{Id: connection.ID, BaseUrl: connection.BaseURL, ApiKey: connection.APIKeyRef, Config: config}, format, nil
}

func ebookRequestDescriptor(in bookrequests.DispatchInput) *pluginv1.RequestDescriptor {
	return &pluginv1.RequestDescriptor{MediaType: "book", Title: in.Request.Title, ExternalIds: map[string]string{"isbn": in.Request.ISBN, "bloem_request_id": ebookSubmissionIdentity(in)}}
}

func ebookSubmissionIdentity(in bookrequests.DispatchInput) string {
	if in.IdempotencyKey != "" {
		return in.IdempotencyKey
	}
	return in.Request.ID
}

type ebookProviderReceipt struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	ISBN      string `json:"isbn"`
	Format    string `json:"format"`
	RequestID string `json:"request_id"`
}

func validateEbookReceipt(receipt string, in bookrequests.DispatchInput, format string) bool {
	if len(receipt) > 2048 || !strings.HasPrefix(receipt, "bwh1.") {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(receipt, "bwh1."))
	if err != nil {
		return false
	}
	var value ebookProviderReceipt
	if json.Unmarshal(raw, &value) != nil || value.ISBN != in.Request.ISBN || value.Format != format || value.RequestID != ebookSubmissionIdentity(in) {
		return false
	}
	switch value.Kind {
	case "unknown", "presence":
		return value.ID == ""
	case "monitoring", "book":
		return value.ID != "" && len(value.ID) <= 128 && !strings.ContainsAny(value.ID, "\r\n\x00")
	default:
		return false
	}
}

func ebookProviderStatus(status, external string) (string, error) {
	if external == "unknown" {
		return "unknown", nil
	}
	switch status {
	case "queued", "searching", "downloading", "completed", "failed":
		return status, nil
	default:
		return "", errors.New("ebook fulfillment provider returned an invalid status")
	}
}

func (a bloemEbookRouter) Fulfill(ctx context.Context, in bookrequests.DispatchInput) (bookrequests.Receipt, error) {
	connection, format, err := a.connection(ctx, in)
	if err != nil {
		return bookrequests.Receipt{}, err
	}
	client, err := a.resolver.RequestRouterClient(ctx, in.Integration.InstallationID, in.Integration.CapabilityID)
	if err != nil {
		return bookrequests.Receipt{}, errors.New("ebook fulfillment plugin unavailable")
	}
	if err = a.validator.ValidateEbookIntegration(ctx, in.Integration); err != nil {
		return bookrequests.Receipt{}, err
	}
	response, err := client.Fulfill(ctx, &pluginv1.FulfillRequest{CapabilityId: in.Integration.CapabilityID, Request: ebookRequestDescriptor(in), Connections: []*pluginv1.RouterConnection{connection}, Qualities: []*pluginv1.RequestedQuality{{Id: format}}})
	if err != nil {
		return bookrequests.Receipt{}, errors.New("ebook fulfillment submission outcome is uncertain")
	}
	if response == nil || len(response.Targets) != 1 {
		return bookrequests.Receipt{}, errors.New("ebook fulfillment provider returned an invalid receipt")
	}
	target := response.Targets[0]
	if target == nil || target.ConnectionId != connection.Id || target.Quality != format || !validateEbookReceipt(target.ExternalId, in, format) {
		return bookrequests.Receipt{}, errors.New("ebook fulfillment provider returned an invalid receipt")
	}
	providerStatus, err := ebookProviderStatus(target.Status, target.ExternalStatus)
	if err != nil {
		return bookrequests.Receipt{}, err
	}
	return bookrequests.Receipt{ExternalID: target.ExternalId, Status: providerStatus}, nil
}

// Recover never submits. A durable ISBN-bound unknown receipt asks CheckStatus
// to look for a prior side effect after process loss or an ambiguous response.
func (a bloemEbookRouter) Recover(ctx context.Context, in bookrequests.DispatchInput) (bookrequests.Receipt, error) {
	_, format, err := a.connection(ctx, in)
	if err != nil {
		return bookrequests.Receipt{}, err
	}
	raw, err := json.Marshal(ebookProviderReceipt{Kind: "unknown", ISBN: in.Request.ISBN, Format: format, RequestID: ebookSubmissionIdentity(in)})
	if err != nil {
		return bookrequests.Receipt{}, err
	}
	return bookrequests.Receipt{ExternalID: "bwh1." + base64.RawURLEncoding.EncodeToString(raw), Status: "unknown"}, nil
}

func (a bloemEbookRouter) CheckStatus(ctx context.Context, in bookrequests.DispatchInput, receipt bookrequests.Receipt) (bookrequests.ProviderStatus, error) {
	connection, format, err := a.connection(ctx, in)
	if err != nil {
		return bookrequests.ProviderStatus{}, err
	}
	if !validateEbookReceipt(receipt.ExternalID, in, format) {
		return bookrequests.ProviderStatus{}, bookrequests.ErrInvalid
	}
	client, err := a.resolver.RequestRouterClient(ctx, in.Integration.InstallationID, in.Integration.CapabilityID)
	if err != nil {
		return bookrequests.ProviderStatus{}, errors.New("ebook fulfillment plugin unavailable")
	}
	if err = a.validator.ValidateEbookIntegration(ctx, in.Integration); err != nil {
		return bookrequests.ProviderStatus{}, err
	}
	response, err := client.CheckStatus(ctx, &pluginv1.CheckStatusRequest{CapabilityId: in.Integration.CapabilityID, Request: ebookRequestDescriptor(in), Connections: []*pluginv1.RouterConnection{connection}, Targets: []*pluginv1.TargetRef{{Quality: format, ConnectionId: connection.Id, ExternalId: receipt.ExternalID}}})
	if err != nil {
		return bookrequests.ProviderStatus{}, errors.New("ebook fulfillment status unavailable")
	}
	if response == nil || len(response.Statuses) != 1 {
		return bookrequests.ProviderStatus{}, errors.New("ebook fulfillment provider returned an invalid status")
	}
	result := response.Statuses[0]
	if result == nil || result.ConnectionId != connection.Id || result.Quality != format {
		return bookrequests.ProviderStatus{}, errors.New("ebook fulfillment provider returned an invalid status")
	}
	providerStatus, err := ebookProviderStatus(result.Status, result.ExternalStatus)
	return bookrequests.ProviderStatus{Status: providerStatus}, err
}
