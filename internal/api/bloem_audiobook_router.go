package api

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/requests"
)

type audiobookIntegrationValidator interface {
	ValidateAudiobookIntegration(context.Context, bookrequests.AudiobookIntegration) error
}
type bloemAudiobookRouter struct {
	integrations ebookIntegrationReader
	resolver     requests.RouterClientResolver
	validator    audiobookIntegrationValidator
}

func (a bloemAudiobookRouter) connection(ctx context.Context, in bookrequests.AudiobookDispatchInput) (*pluginv1.RouterConnection, error) {
	if a.integrations == nil || a.resolver == nil || a.validator == nil || in.Request.MediaType != "audiobook" || in.SubmissionID == "" || in.Attempt < 1 {
		return nil, bookrequests.ErrInvalid
	}
	if err := a.validator.ValidateAudiobookIntegration(ctx, in.Integration); err != nil {
		return nil, err
	}
	c, err := a.integrations.GetIntegration(ctx, in.Integration.ID)
	if err != nil {
		return nil, errors.New("audiobook acquisition connection unavailable")
	}
	if c == nil || !c.Enabled || c.ID != in.Integration.ID || c.InstallationID == nil || *c.InstallationID != in.Integration.InstallationID || c.CapabilityID != in.Integration.CapabilityID || !slices.Equal(c.SupportedMediaTypes, []string{"audiobook"}) || c.APIKeyRef == "" {
		return nil, bookrequests.ErrConflict
	}
	if _, exists := c.PluginConfig["service_kind"]; exists {
		return nil, bookrequests.ErrInvalid
	}
	config, err := structpb.NewStruct(c.PluginConfig)
	if err != nil {
		return nil, bookrequests.ErrInvalid
	}
	return &pluginv1.RouterConnection{Id: c.ID, BaseUrl: c.BaseURL, ApiKey: c.APIKeyRef, Config: config}, nil
}
func audiobookRequestDescriptor(in bookrequests.AudiobookDispatchInput) *pluginv1.RequestDescriptor {
	ids := map[string]string{"bloem_request_id": in.SubmissionID, "bloem_attempt": strconv.Itoa(in.Attempt), "bloem_search_pass": strconv.Itoa(max(in.SearchPass, 1)), "bloem_ai_calls": strconv.Itoa(in.AICalls), "language": in.Integration.Language}
	if ids["language"] == "" {
		ids["language"] = "en"
	}
	for key, value := range map[string]string{"author": in.Request.Author, "isbn": in.Request.ISBN, "asin": in.Request.ASIN, "bloem_excluded_refs": strings.Join(in.ExcludedRefs, ","), "bloem_approved_candidate": in.ApprovedCandidate} {
		if value != "" {
			ids[key] = value
		}
	}
	return &pluginv1.RequestDescriptor{MediaType: "audiobook", Title: in.Request.Title, ExternalIds: ids}
}
func (a bloemAudiobookRouter) Fulfill(ctx context.Context, in bookrequests.AudiobookDispatchInput) (bookrequests.AudiobookOutcome, error) {
	connection, err := a.connection(ctx, in)
	if err != nil {
		return bookrequests.AudiobookOutcome{}, errors.Join(bookrequests.ErrProviderNotCalled, err)
	}
	client, err := a.resolver.RequestRouterClient(ctx, in.Integration.InstallationID, in.Integration.CapabilityID)
	if err != nil {
		return bookrequests.AudiobookOutcome{}, errors.Join(bookrequests.ErrProviderNotCalled, errors.New("audiobook acquisition plugin unavailable"))
	}
	if err = a.validator.ValidateAudiobookIntegration(ctx, in.Integration); err != nil {
		return bookrequests.AudiobookOutcome{}, errors.Join(bookrequests.ErrProviderNotCalled, err)
	}
	response, err := client.Fulfill(ctx, &pluginv1.FulfillRequest{CapabilityId: in.Integration.CapabilityID, Request: audiobookRequestDescriptor(in), Connections: []*pluginv1.RouterConnection{connection}, Qualities: []*pluginv1.RequestedQuality{{Id: "audiobook"}}})
	if err != nil {
		if status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.InvalidArgument {
			return bookrequests.AudiobookOutcome{Kind: bookrequests.AudiobookConfigRejected}, nil
		}
		return bookrequests.AudiobookOutcome{}, errors.New("audiobook acquisition submission outcome is uncertain")
	}
	if response == nil {
		return bookrequests.AudiobookOutcome{}, bookrequests.ErrInvalid
	}
	decision, err := bookrequests.ParseAcquisitionDecision(response.Message)
	if err != nil || decision.Phase != "select" {
		return bookrequests.AudiobookOutcome{}, bookrequests.ErrInvalid
	}
	out := bookrequests.AudiobookOutcome{Decision: decision}
	if len(response.Targets) == 0 {
		switch decision.Outcome {
		case "no_match":
			out.Kind = bookrequests.AudiobookNoMatch
		case "held":
			out.Kind = bookrequests.AudiobookHeld
		case "rejected":
			out.Kind = bookrequests.AudiobookRejected
		case "unavailable":
			out.Kind = bookrequests.AudiobookUnavailable
		default:
			return bookrequests.AudiobookOutcome{}, bookrequests.ErrInvalid
		}
		return out, nil
	}
	if len(response.Targets) != 1 || (decision.Outcome != "handed_off" && decision.Outcome != "adopted") {
		return bookrequests.AudiobookOutcome{}, bookrequests.ErrInvalid
	}
	t := response.Targets[0]
	if t == nil || t.ConnectionId != connection.Id || t.Quality != "audiobook" || (t.Status != "queued" && t.Status != "downloading") || !bookrequests.ValidAudiobookExternalStatus(t.ExternalStatus) {
		return bookrequests.AudiobookOutcome{}, bookrequests.ErrInvalid
	}
	if _, err = bookrequests.ParseAudiobookReceipt(t.ExternalId, in.SubmissionID, in.Attempt); err != nil {
		return bookrequests.AudiobookOutcome{}, bookrequests.ErrInvalid
	}
	out.Kind, out.Receipt, out.ProviderStatus, out.ExternalStatus = bookrequests.AudiobookHandedOff, bookrequests.Receipt{ExternalID: t.ExternalId, Status: t.Status}, t.Status, t.ExternalStatus
	return out, nil
}
func (a bloemAudiobookRouter) Recover(_ context.Context, in bookrequests.AudiobookDispatchInput) (bookrequests.Receipt, error) {
	return bookrequests.RecoveryReceipt(in.SubmissionID, in.Attempt), nil
}
func (a bloemAudiobookRouter) CheckStatus(ctx context.Context, in bookrequests.AudiobookDispatchInput, receipt bookrequests.Receipt) (bookrequests.AudiobookOutcome, error) {
	connection, err := a.connection(ctx, in)
	if err != nil {
		return bookrequests.AudiobookOutcome{}, err
	}
	if _, err = bookrequests.ParseAudiobookReceipt(receipt.ExternalID, in.SubmissionID, in.Attempt); err != nil {
		return bookrequests.AudiobookOutcome{}, bookrequests.ErrInvalid
	}
	client, err := a.resolver.RequestRouterClient(ctx, in.Integration.InstallationID, in.Integration.CapabilityID)
	if err != nil {
		return bookrequests.AudiobookOutcome{}, errors.New("audiobook acquisition plugin unavailable")
	}
	if err = a.validator.ValidateAudiobookIntegration(ctx, in.Integration); err != nil {
		return bookrequests.AudiobookOutcome{}, err
	}
	response, err := client.CheckStatus(ctx, &pluginv1.CheckStatusRequest{CapabilityId: in.Integration.CapabilityID, Request: audiobookRequestDescriptor(in), Connections: []*pluginv1.RouterConnection{connection}, Targets: []*pluginv1.TargetRef{{Quality: "audiobook", ConnectionId: connection.Id, ExternalId: receipt.ExternalID}}})
	if err != nil {
		return bookrequests.AudiobookOutcome{}, errors.New("audiobook acquisition status unavailable")
	}
	if response == nil || len(response.Statuses) != 1 {
		return bookrequests.AudiobookOutcome{}, bookrequests.ErrInvalid
	}
	t := response.Statuses[0]
	if t == nil || t.ConnectionId != connection.Id || t.Quality != "audiobook" || !bookrequests.ValidAudiobookExternalStatus(t.ExternalStatus) || !slices.Contains([]string{"queued", "downloading", "completed", "review", "failed", "unknown"}, t.Status) {
		return bookrequests.AudiobookOutcome{}, bookrequests.ErrInvalid
	}
	out := bookrequests.AudiobookOutcome{Kind: bookrequests.AudiobookStatus, ProviderStatus: audiobookProviderStatus(t.Status, t.ExternalStatus), ExternalStatus: t.ExternalStatus}
	if t.Message != "" {
		out.Decision, err = bookrequests.ParseAcquisitionDecision(t.Message)
		if err != nil || out.Decision.Phase != "verify" {
			return bookrequests.AudiobookOutcome{}, bookrequests.ErrInvalid
		}
	}
	if t.Status == "completed" && (t.ExternalStatus != "verified" || out.Decision == nil || out.Decision.Outcome != "verified") {
		return bookrequests.AudiobookOutcome{}, bookrequests.ErrInvalid
	}
	if t.ExternalStatus == "review" && (out.Decision == nil || out.Decision.Outcome != "review") {
		return bookrequests.AudiobookOutcome{}, bookrequests.ErrInvalid
	}
	return out, nil
}

// audiobookProviderStatus maps the plugin's CheckStatus answer onto the
// dispatch states. Release builds answer in the host's vocabulary: "review",
// and "unknown" with external status "missing", "absent" or "unknown".
// Pre-release builds answered "queued" for all of these, so a queued answer is
// also mapped by its external status. External status "unknown" means the
// plugin could not read the download client; that check never counts toward
// a lost download.
func audiobookProviderStatus(status, external string) string {
	switch {
	case external == "unknown" && (status == "queued" || status == "unknown"):
		return bookrequests.AudiobookProviderUnreadable
	case status != "queued":
		return status
	case external == "review":
		return "review"
	case external == "missing" || external == "absent":
		return "unknown"
	}
	return status
}
