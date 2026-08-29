package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
)

const (
	digestA = "1111111111111111111111111111111111111111111111111111111111111111"
	digestB = "2222222222222222222222222222222222222222222222222222222222222222"
	digestC = "3333333333333333333333333333333333333333333333333333333333333333"
)

type fakeRollout struct {
	recorded  lifecycleidempotency.ClientEvidence
	finalized lifecycleidempotency.FinalizeInput
	status    lifecycleidempotency.RolloutStatus
}

func (f *fakeRollout) RecordClientEvidence(_ context.Context, evidence lifecycleidempotency.ClientEvidence) error {
	f.recorded = evidence
	return nil
}

func (f *fakeRollout) Status(context.Context) (lifecycleidempotency.RolloutStatus, error) {
	return f.status, nil
}

func (f *fakeRollout) Finalize(_ context.Context, input lifecycleidempotency.FinalizeInput) error {
	f.finalized = input
	return nil
}

func TestRunRecordClientValidatesThenRecordsImmutableEvidence(t *testing.T) {
	t.Parallel()
	fake := &fakeRollout{}
	opened := 0
	out := &bytes.Buffer{}
	err := run(context.Background(), []string{
		"record-client",
		"--client", "web",
		"--commit-sha", strings.Repeat("a", 40),
		"--suite-digest", digestA,
		"--released-at", "2026-08-29T09:30:00Z",
		"--release-channel-digest", digestB,
	}, func(key string) string {
		if key == "DATABASE_URL" {
			return "postgres://operator@example/bloem"
		}
		return ""
	}, out, func(_ context.Context, databaseURL string) (rolloutOperations, func(), error) {
		opened++
		if databaseURL != "postgres://operator@example/bloem" {
			t.Fatalf("database URL = %q", databaseURL)
		}
		return fake, func() {}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if opened != 1 {
		t.Fatalf("database opened %d times", opened)
	}
	if fake.recorded.Client != "web" || fake.recorded.CommitSHA != strings.Repeat("a", 40) {
		t.Fatalf("recorded evidence = %#v", fake.recorded)
	}
	if fake.recorded.ReleasedAt.UTC() != time.Date(2026, 8, 29, 9, 30, 0, 0, time.UTC) {
		t.Fatalf("released at = %s", fake.recorded.ReleasedAt)
	}
	if got := out.String(); got != "{\"client\":\"web\",\"recorded\":true}\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestRunRejectsInvalidEvidenceBeforeOpeningDatabase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{"client", []string{"record-client", "--client", "desktop", "--commit-sha", strings.Repeat("a", 40), "--suite-digest", digestA, "--released-at", "2026-08-29T09:30:00Z", "--release-channel-digest", digestB}},
		{"commit", []string{"record-client", "--client", "web", "--commit-sha", strings.Repeat("A", 40), "--suite-digest", digestA, "--released-at", "2026-08-29T09:30:00Z", "--release-channel-digest", digestB}},
		{"digest", []string{"record-client", "--client", "web", "--commit-sha", strings.Repeat("a", 40), "--suite-digest", strings.Repeat("0", 64), "--released-at", "2026-08-29T09:30:00Z", "--release-channel-digest", digestB}},
		{"released at", []string{"record-client", "--client", "web", "--commit-sha", strings.Repeat("a", 40), "--suite-digest", digestA, "--released-at", "yesterday", "--release-channel-digest", digestB}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opened := false
			err := run(context.Background(), tt.args, func(string) string { return "postgres://unused" }, &bytes.Buffer{}, func(context.Context, string) (rolloutOperations, func(), error) {
				opened = true
				return nil, nil, errors.New("must not open")
			})
			if err == nil {
				t.Fatal("expected validation error")
			}
			if opened {
				t.Fatal("opened database for invalid evidence")
			}
		})
	}
}

func TestRunStatusPrintsStableHexJSON(t *testing.T) {
	t.Parallel()
	fake := &fakeRollout{status: lifecycleidempotency.RolloutStatus{
		Phase:                 lifecycleidempotency.PhaseOptional,
		FinalizedRouteDigest:  mustDigest(t, digestA),
		FinalizedSchemaDigest: mustDigest(t, digestB),
		Evidence: []lifecycleidempotency.ClientEvidence{{
			Client: "web", CommitSHA: strings.Repeat("c", 40), SuiteDigest: mustDigest(t, digestB),
			ReleasedAt:           time.Date(2026, 8, 29, 9, 30, 0, 0, time.UTC),
			ReleaseChannelDigest: mustDigest(t, digestC), RecordedAt: time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC),
		}},
	}}
	out := &bytes.Buffer{}
	err := run(context.Background(), []string{"status"}, func(string) string { return "postgres://unused" }, out,
		func(context.Context, string) (rolloutOperations, func(), error) { return fake, func() {}, nil })
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{`"phase":"optional"`, `"finalized_route_digest":"` + digestA + `"`, `"suite_digest":"` + digestB + `"`, `"release_channel_digest":"` + digestC + `"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q does not contain %q", got, want)
		}
	}
}

func TestRunFinalizeRequiresConfirmationAndMatchingDigestsBeforeOpeningDatabase(t *testing.T) {
	t.Parallel()
	base := []string{"finalize", "--observed-route-digest", digestA, "--expected-route-digest", digestA, "--observed-schema-digest", digestB, "--expected-schema-digest", digestB, "--production-web-digest", digestC}
	for _, args := range [][]string{
		base,
		append(append([]string{}, base...), "--confirm", "optional"),
		{"finalize", "--confirm", "required", "--observed-route-digest", digestA, "--expected-route-digest", digestB, "--observed-schema-digest", digestB, "--expected-schema-digest", digestB, "--production-web-digest", digestC},
	} {
		opened := false
		err := run(context.Background(), args, func(string) string { return "postgres://unused" }, &bytes.Buffer{}, func(context.Context, string) (rolloutOperations, func(), error) {
			opened = true
			return nil, nil, errors.New("must not open")
		})
		if err == nil || opened {
			t.Fatalf("err = %v, opened = %v", err, opened)
		}
	}
}

func TestRunFinalizePassesValidatedEvidenceDigests(t *testing.T) {
	t.Parallel()
	fake := &fakeRollout{}
	err := run(context.Background(), []string{"finalize", "--confirm", "required", "--observed-route-digest", digestA, "--expected-route-digest", digestA, "--observed-schema-digest", digestB, "--expected-schema-digest", digestB, "--production-web-digest", digestC},
		func(string) string { return "postgres://unused" }, &bytes.Buffer{}, func(context.Context, string) (rolloutOperations, func(), error) { return fake, func() {}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if fake.finalized.ExpectedRouteDigest != mustDigest(t, digestA) || fake.finalized.ProductionWebDigest != mustDigest(t, digestC) {
		t.Fatalf("finalize input = %#v", fake.finalized)
	}
}

func mustDigest(t *testing.T, value string) lifecycleidempotency.Digest {
	t.Helper()
	digest, err := parseDigest(value)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
