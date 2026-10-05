package policy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

// These tests guard the admission boundary: rejected tenant contexts cannot
// reach the checker, and accepted requests preserve the upstream action facts.
func TestBloemPlaybackAdmissionRejectsUntrustedTenant(t *testing.T) {
	type testCase struct {
		name   string
		ctx    context.Context
		userID int
	}
	tests := []testCase{
		{name: "nil context", userID: 1},
		{name: "missing", ctx: context.Background(), userID: 1},
		{name: "mismatched user", ctx: resolvedTenantContextForPolicyTest(), userID: 2},
		{name: "nonpositive user", ctx: resolvedTenantContextForPolicyTest(), userID: 0},
	}
	for _, status := range []string{"organization", "membership"} {
		tenant := resolvedTenantForPolicyTest()
		if status == "organization" {
			tenant.OrganizationStatus = tenancy.OrganizationSuspended
		} else {
			tenant.MembershipStatus = tenancy.MembershipSuspended
		}
		tests = append(tests, testCase{
			name:   "inactive " + status,
			ctx:    tenancy.WithContext(context.Background(), tenant),
			userID: 1,
		})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checker := &bloemPlaybackChecker{decision: ActionDecision{Allowed: true}}
			decision, err := NewBloemPlaybackAdmissionDecider(checker)(test.ctx, playback.AdmissionRequest{UserID: test.userID})
			if !errors.Is(err, ErrTenantFactsUnavailable) || !strings.HasPrefix(err.Error(), "playback admission tenant facts: ") {
				t.Fatalf("error = %v, want wrapped tenant facts failure", err)
			}
			if decision != (playback.AdmissionDecision{}) || len(checker.inputs) != 0 {
				t.Fatalf("decision = %+v, checker calls = %d, want zero decision and no evaluation", decision, len(checker.inputs))
			}
		})
	}
}

func TestBloemPlaybackAdmissionPreservesActionFactsAndDecision(t *testing.T) {
	for _, test := range []struct {
		name         string
		method       playback.PlayMethod
		video, audio bool
		wantAction   string
	}{
		{name: "direct", method: playback.PlayDirect, wantAction: "direct_play"},
		{name: "remux", method: playback.PlayRemux, wantAction: "direct_play"},
		{name: "video", method: playback.PlayTranscode, video: true, wantAction: "transcode"},
		{name: "audio", method: playback.PlayRemux, audio: true, wantAction: "audio_transcode"},
		{name: "video takes precedence", method: playback.PlayTranscode, video: true, audio: true, wantAction: "transcode"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, allowed := range []bool{false, true} {
				checker := &bloemPlaybackChecker{decision: ActionDecision{Allowed: allowed, Reason: "policy reason", ReasonCode: "policy_code", QualityCeiling: "720p"}}
				ctx := resolvedTenantContextForPolicyTest()
				req := playback.AdmissionRequest{
					UserID:               1,
					Limits:               playback.SessionLimits{MaxStreams: 9, MaxTranscodes: 4, TranscodingDisabled: !allowed, AudioTranscodingDisabled: allowed},
					CurrentActiveStreams: 5, CurrentActiveTranscodes: 2,
					RequestedMethod: test.method, RequiresVideoTranscode: test.video, RequiresAudioTranscode: test.audio,
				}
				before := time.Now().UTC().Truncate(time.Second)
				got, err := NewBloemPlaybackAdmissionDecider(checker)(ctx, req)
				after := time.Now().UTC()
				if err != nil || got != (playback.AdmissionDecision{Allowed: allowed, Reason: "policy reason", ReasonCode: "policy_code"}) {
					t.Fatalf("decision = %+v, error = %v", got, err)
				}
				if len(checker.inputs) != 1 || checker.contexts[0] != ctx {
					t.Fatalf("checker calls = %d, want one with original context", len(checker.inputs))
				}
				input := checker.inputs[0]
				requestTime, err := time.Parse(time.RFC3339, input.RequestTime)
				if err != nil || !strings.HasSuffix(input.RequestTime, "Z") || requestTime.Before(before) || requestTime.After(after) {
					t.Fatalf("request time = %q, error = %v, want current UTC time", input.RequestTime, err)
				}
				input.RequestTime = ""
				want := ActionInput{
					SchemaVersion: 1, Tenant: validLegacyTenantFactsForPolicyTest(), Action: "playback_admission", UserID: 1,
					MaxStreams: 9, MaxTranscodes: 4, TranscodeAllowed: allowed, AudioTranscodeAllowed: !allowed,
					CurrentActiveStreams: 5, CurrentActiveTranscodes: 2, RequestedAction: test.wantAction,
				}
				if !reflect.DeepEqual(input, want) {
					t.Fatalf("input = %+v, want %+v", input, want)
				}
			}
		})
	}
}

func TestBloemPlaybackAdmissionPropagatesCheckerError(t *testing.T) {
	checkerErr := errors.New("policy evaluation failed")
	checker := &bloemPlaybackChecker{decision: ActionDecision{Allowed: true, Reason: "must not leak"}, err: checkerErr}
	got, err := NewBloemPlaybackAdmissionDecider(checker)(resolvedTenantContextForPolicyTest(), playback.AdmissionRequest{UserID: 1})
	if !errors.Is(err, checkerErr) || got != (playback.AdmissionDecision{}) || len(checker.inputs) != 1 {
		t.Fatalf("decision = %+v, error = %v, calls = %d, want zero decision and unchanged checker error", got, err, len(checker.inputs))
	}
}

type bloemPlaybackChecker struct {
	inputs   []ActionInput
	contexts []context.Context
	decision ActionDecision
	meta     Meta
	err      error
}

func (c *bloemPlaybackChecker) CheckAction(ctx context.Context, input ActionInput) (ActionDecision, Meta, error) {
	c.inputs = append(c.inputs, input)
	c.contexts = append(c.contexts, ctx)
	return c.decision, c.meta, c.err
}

// Caller-supplied tenant facts must never become policy authority. The wrapper
// replaces even a complete tenant document, while preserving all other fields.
func TestBloemPlaybackActionCheckerReplacesCallerTenantFacts(t *testing.T) {
	tenant := resolvedTenantForPolicyTest()
	tenant.OrganizationID = uuid.MustParse("30000000-0000-0000-0000-000000000003")
	tenant.MembershipID = uuid.MustParse("40000000-0000-0000-0000-000000000004")
	tenant.Legacy = false
	tenant.OrganizationDefault = false
	tenant.OrganizationStatus = tenancy.OrganizationActive
	tenant.PolicyRevision = 17
	tenant.SecurityRevision = 23
	ctx := tenancy.WithContext(context.Background(), tenant)
	input := ActionInput{
		SchemaVersion: 1, Tenant: validLegacyTenantFactsForPolicyTest(), UserID: 1,
		Action: "playback_admission", MaxStreams: 9, MaxTranscodes: 4,
		CurrentActiveStreams: 5, CurrentActiveTranscodes: 2,
		RequestedAction: "transcode", RequestTime: "2026-10-05T00:00:00Z",
		DeviceID: "device", ClientIP: "192.0.2.1", RequestedQuality: "1080p",
	}
	checkerErr := errors.New("inner error")
	checker := &bloemPlaybackChecker{
		decision: ActionDecision{Allowed: true, Reason: "custom", ReasonCode: "custom_code", QualityCeiling: "720p"},
		meta:     Meta{DecisionName: DecisionName("custom decision"), EvalTimeNS: 31, Revision: 41},
		err:      checkerErr,
	}
	got, meta, err := (bloemPlaybackActionChecker{checker: checker}).CheckAction(ctx, input)
	if got != checker.decision || meta != checker.meta || !errors.Is(err, checkerErr) {
		t.Fatalf("result = (%+v, %+v, %v), want unchanged checker result", got, meta, err)
	}
	if len(checker.inputs) != 1 || checker.contexts[0] != ctx {
		t.Fatalf("checker calls = %d, want one with original context", len(checker.inputs))
	}
	want := input
	want.Tenant = TenantFacts{
		Present: true, Legacy: false,
		OrganizationID:     "30000000-0000-0000-0000-000000000003",
		MembershipID:       "40000000-0000-0000-0000-000000000004",
		OrganizationStatus: "active", MembershipStatus: "active",
		OrganizationPolicyRevision: 17, MembershipSecurityRevision: 23,
	}
	if !reflect.DeepEqual(checker.inputs[0], want) {
		t.Fatalf("input = %+v, want trusted context tenant with other facts preserved %+v", checker.inputs[0], want)
	}
	if input.Tenant != validLegacyTenantFactsForPolicyTest() {
		t.Fatalf("caller input mutated: %+v", input)
	}
}
