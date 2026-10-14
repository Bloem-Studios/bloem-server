package api

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/requests"
)

// requestConnectionTestTimeout bounds one plugin connection test. The
// audiobook plugin budgets 25 s for its own checks.
const requestConnectionTestTimeout = 40 * time.Second

const maxConnectionTestMessage = 300

// checkRequestConnection asks the connection's request_router plugin to test
// the stored settings. The plugin's verdict is returned as data: only a
// missing router is an error. Transport errors are replaced by fixed text, and
// a plugin message is withheld if it repeats any credential in secrets.
func checkRequestConnection(ctx context.Context, router requests.RequestRouterProvider, in requests.Integration, secrets []string) (handlers.RequestIntegrationTestResult, error) {
	if router == nil || in.InstallationID == nil {
		return handlers.RequestIntegrationTestResult{}, errors.New("request connection test unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, requestConnectionTestTimeout)
	defer cancel()
	ok, message, err := router.TestConnection(ctx, *in.InstallationID, in.CapabilityID, requests.ResolvedRouterConnection{ID: in.ID, BaseURL: in.BaseURL, APIKey: in.APIKeyRef, Config: in.PluginConfig})
	if err != nil {
		return handlers.RequestIntegrationTestResult{OK: false, Message: connectionTestFailure(ctx, err)}, nil
	}
	return handlers.RequestIntegrationTestResult{OK: ok, Message: connectionTestMessage(ok, message, secrets)}, nil
}

func connectionTestFailure(ctx context.Context, err error) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded:
		return "The connection test timed out."
	case status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.InvalidArgument:
		return "The request plugin rejected the saved connection settings."
	case status.Code(err) == codes.Unimplemented:
		return "This request plugin version cannot test connections."
	default:
		return "The request plugin could not run the connection test. Check that it is enabled and running."
	}
}

func connectionTestMessage(ok bool, message string, secrets []string) string {
	if message = pluginMessage(message, secrets); message != "" {
		return message
	}
	if ok {
		return "Connection test passed."
	}
	return "Connection test failed."
}

// pluginMessage makes plugin-authored text safe to show an administrator: one
// line, bounded, and withheld entirely if it repeats a saved credential. Each
// credential is matched against the raw text and, with runs of space and
// control characters collapsed on both sides, against the shown line, so a
// credential containing whitespace cannot pass through the collapsing.
func pluginMessage(message string, secrets []string) string {
	shown := oneLine(message)
	if !utf8.ValidString(shown) {
		return ""
	}
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		if collapsed := oneLine(secret); strings.Contains(message, secret) || collapsed != "" && strings.Contains(shown, collapsed) {
			return "The plugin's message was withheld because it contained a saved credential."
		}
	}
	if runes := []rune(shown); len(runes) > maxConnectionTestMessage {
		shown = string(runes[:maxConnectionTestMessage]) + "…"
	}
	return shown
}

// oneLine collapses each run of space and control characters to one space.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }), " ")
}
