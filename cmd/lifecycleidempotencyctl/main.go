// Command lifecycleidempotencyctl records and verifies the immutable evidence
// used to make lifecycle idempotency mandatory.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	databaseURLEnvironment = "DATABASE_URL"
	commandRecordClient    = "record-client"
	commandFinalize        = "finalize"
	clientWeb              = "web"
	clientApple            = "apple"
	clientAndroid          = "android"
)

var commitSHAFormat = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

type rolloutOperations interface {
	RecordClientEvidence(context.Context, lifecycleidempotency.ClientEvidence) error
	Status(context.Context) (lifecycleidempotency.RolloutStatus, error)
	Finalize(context.Context, lifecycleidempotency.FinalizeInput) error
}

type rolloutOpener func(context.Context, string) (rolloutOperations, func(), error)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Getenv, os.Stdout, openRollout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "lifecycle-idempotency:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, output io.Writer, open rolloutOpener) error {
	if len(args) == 0 {
		return errors.New("command required: record-client, status, or finalize")
	}

	switch args[0] {
	case commandRecordClient:
		evidence, err := parseRecordClient(args[1:])
		if err != nil {
			return err
		}
		rollout, closeRollout, err := connect(ctx, getenv, open)
		if err != nil {
			return err
		}
		defer closeRollout()
		if err := rollout.RecordClientEvidence(ctx, evidence); err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(struct {
			Client   string `json:"client"`
			Recorded bool   `json:"recorded"`
		}{Client: evidence.Client, Recorded: true})
	case "status":
		if len(args) != 1 {
			return errors.New("status accepts no arguments")
		}
		rollout, closeRollout, err := connect(ctx, getenv, open)
		if err != nil {
			return err
		}
		defer closeRollout()
		status, err := rollout.Status(ctx)
		if err != nil {
			return err
		}
		return writeStatus(output, status)
	case commandFinalize:
		input, err := parseFinalize(args[1:])
		if err != nil {
			return err
		}
		rollout, closeRollout, err := connect(ctx, getenv, open)
		if err != nil {
			return err
		}
		defer closeRollout()
		if err := rollout.Finalize(ctx, input); err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(struct {
			Phase lifecycleidempotency.Phase `json:"phase"`
		}{Phase: lifecycleidempotency.PhaseRequired})
	default:
		return fmt.Errorf("unknown command %q: expected record-client, status, or finalize", args[0])
	}
}

func connect(ctx context.Context, getenv func(string) string, open rolloutOpener) (rolloutOperations, func(), error) {
	databaseURL := strings.TrimSpace(getenv(databaseURLEnvironment))
	if databaseURL == "" {
		return nil, nil, fmt.Errorf("%s is required", databaseURLEnvironment)
	}
	rollout, closeRollout, err := open(ctx, databaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("connect lifecycle rollout database: %w", err)
	}
	return rollout, closeRollout, nil
}

func openRollout(ctx context.Context, databaseURL string) (rolloutOperations, func(), error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, nil, err
	}
	return lifecycleidempotency.NewRollout(pool, api.LifecycleRouteDigest()), pool.Close, nil
}

func parseRecordClient(args []string) (lifecycleidempotency.ClientEvidence, error) {
	flags := newFlagSet("record-client")
	client := flags.String("client", "", "client name: web, apple, or android")
	commitSHA := flags.String("commit-sha", "", "released Git commit SHA")
	suiteDigestText := flags.String("suite-digest", "", "SHA-256 of the passing release suite evidence")
	releasedAtText := flags.String("released-at", "", "release timestamp in RFC3339 format")
	channelDigestText := flags.String("release-channel-digest", "", "SHA-256 identifying the released channel artifact")
	if err := flags.Parse(args); err != nil {
		return lifecycleidempotency.ClientEvidence{}, err
	}
	if flags.NArg() != 0 {
		return lifecycleidempotency.ClientEvidence{}, errors.New("record-client accepts flags only")
	}
	if *client != clientWeb && *client != clientApple && *client != clientAndroid {
		return lifecycleidempotency.ClientEvidence{}, errors.New("client must be web, apple, or android")
	}
	if !commitSHAFormat.MatchString(*commitSHA) {
		return lifecycleidempotency.ClientEvidence{}, errors.New("commit-sha must be a lowercase 40- or 64-character hexadecimal Git object ID")
	}
	suiteDigest, err := parseNamedDigest("suite-digest", *suiteDigestText)
	if err != nil {
		return lifecycleidempotency.ClientEvidence{}, err
	}
	channelDigest, err := parseNamedDigest("release-channel-digest", *channelDigestText)
	if err != nil {
		return lifecycleidempotency.ClientEvidence{}, err
	}
	releasedAt, err := time.Parse(time.RFC3339, *releasedAtText)
	if err != nil {
		return lifecycleidempotency.ClientEvidence{}, fmt.Errorf("released-at must be RFC3339: %w", err)
	}
	return lifecycleidempotency.ClientEvidence{
		Client: *client, CommitSHA: *commitSHA, SuiteDigest: suiteDigest,
		ReleasedAt: releasedAt, ReleaseChannelDigest: channelDigest,
	}, nil
}

func parseFinalize(args []string) (lifecycleidempotency.FinalizeInput, error) {
	flags := newFlagSet("finalize")
	confirm := flags.String("confirm", "", "must be 'required' to acknowledge the irreversible phase change")
	expectedRoute := flags.String("expected-route-digest", "", "reviewed route-manifest SHA-256")
	expectedSchema := flags.String("expected-schema-digest", "", "reviewed schema SHA-256")
	productionWeb := flags.String("production-web-digest", "", "production web release-channel SHA-256")
	if err := flags.Parse(args); err != nil {
		return lifecycleidempotency.FinalizeInput{}, err
	}
	if flags.NArg() != 0 {
		return lifecycleidempotency.FinalizeInput{}, errors.New("finalize accepts flags only")
	}
	if *confirm != string(lifecycleidempotency.PhaseRequired) {
		return lifecycleidempotency.FinalizeInput{}, errors.New("finalize is irreversible; pass --confirm required")
	}
	values := []struct {
		name string
		text string
	}{
		{"expected-route-digest", *expectedRoute},
		{"expected-schema-digest", *expectedSchema},
		{"production-web-digest", *productionWeb},
	}
	digests := make([]lifecycleidempotency.Digest, len(values))
	for i, value := range values {
		digest, err := parseNamedDigest(value.name, value.text)
		if err != nil {
			return lifecycleidempotency.FinalizeInput{}, err
		}
		digests[i] = digest
	}
	return lifecycleidempotency.FinalizeInput{
		ExpectedRouteDigest: digests[0], ExpectedSchemaDigest: digests[1],
		ProductionWebDigest: digests[2],
	}, nil
}

func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

func parseNamedDigest(name, value string) (lifecycleidempotency.Digest, error) {
	digest, err := parseDigest(value)
	if err != nil {
		return lifecycleidempotency.Digest{}, fmt.Errorf("%s must be a non-zero lowercase SHA-256 digest: %w", name, err)
	}
	return digest, nil
}

func parseDigest(value string) (lifecycleidempotency.Digest, error) {
	var digest lifecycleidempotency.Digest
	if len(value) != hex.EncodedLen(len(digest)) || strings.ToLower(value) != value {
		return digest, errors.New("invalid encoding")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return digest, errors.New("invalid encoding")
	}
	copy(digest[:], decoded)
	if digest == (lifecycleidempotency.Digest{}) {
		return digest, errors.New("zero digest")
	}
	return digest, nil
}

type statusJSON struct {
	Phase                  lifecycleidempotency.Phase `json:"phase"`
	FinalizedAt            *time.Time                 `json:"finalized_at"`
	FinalizedRouteDigest   string                     `json:"finalized_route_digest"`
	FinalizedSchemaDigest  string                     `json:"finalized_schema_digest"`
	CurrentRouteDigest     string                     `json:"current_route_digest"`
	CurrentSchemaDigest    string                     `json:"current_schema_digest"`
	RouteMatchesFinalized  bool                       `json:"route_matches_finalized"`
	SchemaMatchesFinalized bool                       `json:"schema_matches_finalized"`
	Evidence               []evidenceJSON             `json:"evidence"`
}

type evidenceJSON struct {
	Client               string    `json:"client"`
	CommitSHA            string    `json:"commit_sha"`
	SuiteDigest          string    `json:"suite_digest"`
	ReleasedAt           time.Time `json:"released_at"`
	ReleaseChannelDigest string    `json:"release_channel_digest"`
	RecordedAt           time.Time `json:"recorded_at"`
}

func writeStatus(output io.Writer, status lifecycleidempotency.RolloutStatus) error {
	encoded := statusJSON{
		Phase: status.Phase, FinalizedAt: status.FinalizedAt,
		FinalizedRouteDigest:   hex.EncodeToString(status.FinalizedRouteDigest[:]),
		FinalizedSchemaDigest:  hex.EncodeToString(status.FinalizedSchemaDigest[:]),
		CurrentRouteDigest:     hex.EncodeToString(status.CurrentRouteDigest[:]),
		CurrentSchemaDigest:    hex.EncodeToString(status.CurrentSchemaDigest[:]),
		RouteMatchesFinalized:  status.RouteMatchesFinalized,
		SchemaMatchesFinalized: status.SchemaMatchesFinalized,
		Evidence:               make([]evidenceJSON, 0, len(status.Evidence)),
	}
	for _, evidence := range status.Evidence {
		encoded.Evidence = append(encoded.Evidence, evidenceJSON{
			Client: evidence.Client, CommitSHA: evidence.CommitSHA,
			SuiteDigest: hex.EncodeToString(evidence.SuiteDigest[:]), ReleasedAt: evidence.ReleasedAt,
			ReleaseChannelDigest: hex.EncodeToString(evidence.ReleaseChannelDigest[:]), RecordedAt: evidence.RecordedAt,
		})
	}
	encoder := json.NewEncoder(output)
	return encoder.Encode(encoded)
}
