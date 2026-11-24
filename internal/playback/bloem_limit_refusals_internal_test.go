package playback

import (
	"errors"
	"fmt"
	"testing"
)

func TestClassifyLimitRefusal(t *testing.T) {
	cases := []struct {
		err   error
		kind  LimitKind
		scope LimitScope
	}{
		{ErrHouseholdStreamsExceeded, LimitStreams, ScopeHousehold},
		{ErrHouseholdTranscodesExceeded, LimitTranscodes, ScopeHousehold},
		{ErrTenantTranscodesExceeded, LimitTranscodes, ScopeOrganization},
		{ErrTooManyStreams, LimitStreams, ScopePerson},
		{ErrTooManyTranscodes, LimitTranscodes, ScopePerson},
	}
	for _, tc := range cases {
		wrapped := fmt.Errorf("start playback: %w", tc.err)
		kind, scope, ok := classifyLimitRefusal(wrapped)
		if !ok || kind != tc.kind || scope != tc.scope {
			t.Errorf("classifyLimitRefusal(%v) = %s, %s, %v; want %s, %s", tc.err, kind, scope, ok, tc.kind, tc.scope)
		}
	}
	for _, err := range []error{nil, ErrPlaybackNotAllowed, ErrTranscodingDisabled, errors.New("other")} {
		if _, _, ok := classifyLimitRefusal(err); ok {
			t.Errorf("classifyLimitRefusal(%v) classified a non-limit error", err)
		}
	}
}

func TestClassifyAdmissionReason(t *testing.T) {
	cases := map[string]struct {
		kind  LimitKind
		scope LimitScope
	}{
		AdmissionReasonMaxStreamsExceeded:       {LimitStreams, ScopePerson},
		AdmissionReasonMaxTranscodesExceeded:    {LimitTranscodes, ScopePerson},
		AdmissionReasonTenantTranscodesExceeded: {LimitTranscodes, ScopeOrganization},
	}
	for reason, want := range cases {
		kind, scope, ok := classifyLimitRefusal(admissionDenyError(reason))
		if !ok || kind != want.kind || scope != want.scope {
			t.Errorf("reason %s = %s, %s, %v; want %+v", reason, kind, scope, ok, want)
		}
	}
}
