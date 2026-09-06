package apiv2

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

type NotificationEmailVerificationService interface {
	EmailVerificationAvailable() bool
	QueueEmailVerification(context.Context, int, string, string, string) (notifications.EmailVerificationReceipt, error)
}
type NotificationEmailVerificationInput struct {
	Body struct {
		VerificationID string `json:"verification_id" format:"uuid" doc:"Retained client-created verification intent UUID. Reuse with the original email and authority after uncertainty; never rotate just to retry."`
		Email          string `json:"email" minLength:"1" maxLength:"320"`
	}
}
type NotificationEmailVerificationReceipt struct {
	VerificationID ID     `json:"verification_id"`
	ExpiresAt      string `json:"expires_at" format:"date-time"`
	Current        bool   `json:"current" doc:"The original verification remains pending. False after expiry, clear, verification or replacement; never triggers an automatic resend."`
}
type NotificationEmailVerificationOutput struct {
	Body NotificationEmailVerificationReceipt
}
type NotificationEmailVerificationCapability struct {
	Revision          string `json:"revision"`
	QueueAvailable    bool   `json:"queue_available" doc:"Durable admission is configured; does not assert SMTP delivery or dispatch availability."`
	DispatchAvailable bool   `json:"dispatch_available" doc:"A durable verification dispatcher is configured. This prerequisite currently has none."`
}
type NotificationEmailVerificationCapabilityOutput struct {
	Body NotificationEmailVerificationCapability
}

func registerEmailVerification(reg *Registry) {
	capOp := notificationOperation(http.MethodGet, "/email-preferences/address/capabilities", "getNotificationEmailVerificationCapabilities")
	capOp.Summary = "Describe durable verification admission separately from dispatch availability."
	Register(reg, capOp, func(context.Context, *struct{}) (*NotificationEmailVerificationCapabilityOutput, error) {
		return &NotificationEmailVerificationCapabilityOutput{Body: NotificationEmailVerificationCapability{Revision: "queued_verification_v1", QueueAvailable: reg.deps.NotificationEmailVerification != nil && reg.deps.NotificationEmailVerification.EmailVerificationAvailable(), DispatchAvailable: false}}, nil
	})
	op := notificationOperation(http.MethodPut, "/email-preferences/address", "requestNotificationEmailVerification")
	op.RetrySafety = RetrySafetyDurableDispatch
	op.Errors = []int{409, 429}
	op.MaxBodyBytes = 4096
	op.Summary = "Durably queue one retained verification intent. Receipt is admission, not delivery; exact replay never creates another message."
	Register(reg, op, func(ctx context.Context, in *NotificationEmailVerificationInput) (*NotificationEmailVerificationOutput, error) {
		svc := reg.deps.NotificationEmailVerification
		if svc == nil || !svc.EmailVerificationAvailable() {
			return nil, unavailable("durable email verification")
		}
		user, profile, p := viewerIdentity(ctx)
		if p != nil {
			return nil, p
		}
		receipt, err := svc.QueueEmailVerification(ctx, user, profile, in.Body.VerificationID, in.Body.Email)
		if err != nil {
			switch {
			case errors.Is(err, notifications.ErrEmailChildProfile):
				return nil, NewProblem(TypePermissionDenied, "Child or unavailable profiles cannot change the notification address.")
			case errors.Is(err, notifications.ErrEmailInvalidAddress):
				return nil, NewProblem(TypeValidationFailed, "A valid email and canonical verification UUID are required.")
			case errors.Is(err, notifications.ErrEmailVerificationConflict), errors.Is(err, notifications.ErrEmailAddressInUse), errors.Is(err, notifications.ErrEmailNoLinkBase):
				return nil, NewProblem(TypeConflict, "The original intent, address ownership or external link configuration conflicts.")
			case errors.Is(err, notifications.ErrEmailVerifyRateLimited):
				return nil, NewProblem(TypeRateLimited, "Verification requests are rate limited.")
			case errors.Is(err, notifications.ErrEmailVerificationUnavailable):
				return nil, unavailable("durable email verification")
			default:
				return nil, serviceProblem(err)
			}
		}
		return &NotificationEmailVerificationOutput{Body: NotificationEmailVerificationReceipt{VerificationID: ID(receipt.ID), ExpiresAt: receipt.ExpiresAt.UTC().Format(time.RFC3339Nano), Current: receipt.Current}}, nil
	})
}
