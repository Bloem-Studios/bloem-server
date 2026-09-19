package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func bloemXtreamTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("disposable test database required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	host, name := cfg.ConnConfig.Host, cfg.ConnConfig.Database
	if (host != "127.0.0.1" && host != "localhost" && host != "::1") || (!strings.Contains(name, "test") && !strings.Contains(name, "_ci") && !strings.HasPrefix(name, "bloem_close_")) {
		t.Fatal("Xtream fixtures require an explicitly named, loopback-only disposable test database")
	}
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var migrated bool
	if err := pool.QueryRow(t.Context(), `SELECT to_regclass('bloem_livetv_xtream_credentials') IS NOT NULL AND to_regclass('bloem_livetv_xtream_leases') IS NOT NULL`).Scan(&migrated); err != nil || !migrated {
		t.Fatal("Xtream test database must be fully migrated before this package runs")
	}
	return pool
}

func TestBloemXtreamEncryptedProviderAndConnectionAdmission(t *testing.T) {
	pool := bloemXtreamTestPool(t)
	cipher, err := secret.New([]byte(strings.Repeat("fixture-key-", 4)))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(pool)
	service.SetXtreamCipher(cipher)
	base := "https://provider-" + uuid.NewString() + ".invalid"
	t.Cleanup(func() {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := pool.Exec(ctx, `DELETE FROM livetv_tuners WHERE type='xtream' AND base_url=$1`, base); err != nil {
			t.Error(err)
		}
	})
	packets := make([]byte, 3*188)
	packets[0], packets[188], packets[376] = 0x47, 0x47, 0x47
	lineup := `[{"stream_id":"2147483648","name":"News","epg_channel_id":"news.example"}]`
	streamCalls := 0
	service.xtreamTransport = xtreamRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.Path, "/live/") {
			streamCalls++
			return xtreamFixtureResponse(200, string(packets)), nil
		}
		if r.URL.Query().Get("action") == "get_live_streams" {
			return xtreamFixtureResponse(200, lineup), nil
		}
		return xtreamFixtureResponse(200, `{"user_info":{"auth":1,"status":"Active","max_connections":"1","allowed_output_formats":["ts"]}}`), nil
	})
	input := AddTunerInput{Type: TunerTypeXtream, URL: base, Username: "fixture-account", Password: "fixture-provider-secret", MaxConnections: 4}
	tuner, err := service.AddTuner(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if tuner.Type != TunerTypeXtream || tuner.TunerCount != 1 || tuner.ChannelCount != 1 || tuner.Status != "ready" {
		t.Fatalf("incorrect provider state: %#v", tuner)
	}
	var envelope string
	if err := pool.QueryRow(t.Context(), `SELECT credentials FROM bloem_livetv_xtream_credentials WHERE tuner_id=$1`, tuner.ID).Scan(&envelope); err != nil {
		t.Fatal(err)
	}
	if !secret.IsEncrypted(envelope) || strings.Contains(envelope, input.Password) || strings.Contains(envelope, input.Username) {
		t.Fatal("credentials are not encrypted at rest")
	}
	decoded, err := decryptXtreamCredentials(cipher, tuner, envelope)
	if err != nil || decoded.Username != input.Username || decoded.Password != input.Password {
		t.Fatal("credential round trip failed")
	}
	for _, changed := range []Tuner{{ID: "other-tuner", BaseURL: tuner.BaseURL}, {ID: tuner.ID, BaseURL: "https://other.invalid"}} {
		if _, err := decryptXtreamCredentials(cipher, &changed, envelope); err == nil {
			t.Fatal("credentials were transplantable across source identity/origin")
		}
	}
	input.Password = "replacement-credential"
	if _, err := service.AddTuner(t.Context(), input); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("duplicate account replaced credentials: %v", err)
	}
	channels, err := service.ListChannels(t.Context(), tuner.ID)
	if err != nil || len(channels) != 1 {
		t.Fatal("missing imported channel")
	}
	channel := channels[0]
	if channel.Number != "2147483648" || !strings.HasPrefix(channel.StreamURL, "xtream://") {
		t.Fatal("wide provider identity was truncated or turned into a credential URL")
	}
	wire, err := json.Marshal([]any{tuner, channel})
	if err != nil || strings.Contains(string(wire), "fixture-account") || strings.Contains(string(wire), "fixture-provider-secret") || strings.Contains(string(wire), "xtream://") {
		t.Fatal("public provider/channel response exposed protected input")
	}
	lineup = `[{"stream_id":2147483648,"name":"Renamed news","epg_channel_id":"news.example"}]`
	if err := service.ScanTuner(t.Context(), tuner.ID); err != nil {
		t.Fatal(err)
	}
	channels, err = service.ListChannels(t.Context(), tuner.ID)
	if err != nil || len(channels) != 1 || channels[0].ID != channel.ID || channels[0].Name != "Renamed news" {
		t.Fatal("rescan changed the stable provider channel identity")
	}

	body, err := service.openXtreamChannel(t.Context(), channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = body.Close() })
	otherPool, err := pgxpool.NewWithConfig(t.Context(), pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer otherPool.Close()
	other := NewPgStore(otherPool)
	if err := other.claimXtreamLease(t.Context(), tuner.ID, uuid.NewString()); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("another replica bypassed provider capacity: %v", err)
	}
	if err := service.DeleteTuner(t.Context(), tuner.ID); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("active source deletion bypassed provider ownership: %v", err)
	}
	data, err := io.ReadAll(body)
	if err != nil || string(data) != string(packets) || streamCalls != 1 {
		t.Fatal("protected input did not deliver exactly one MPEG-TS connection")
	}
	_ = body.Close()
	lease := uuid.NewString()
	if err := other.claimXtreamLease(t.Context(), tuner.ID, lease); err != nil {
		t.Fatal(err)
	}
	if err := other.renewXtreamLease(t.Context(), tuner.ID, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE bloem_livetv_xtream_leases SET expires_at=clock_timestamp()-interval '1 second' WHERE lease_id=$1`, lease); err != nil {
		t.Fatal(err)
	}
	replacement := uuid.NewString()
	if err := other.claimXtreamLease(t.Context(), tuner.ID, replacement); err != nil {
		t.Fatal(err)
	}
	if err := other.renewXtreamLease(t.Context(), tuner.ID, lease); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired lease revived")
	}
	if err := other.releaseXtreamLease(t.Context(), tuner.ID, lease); err != nil {
		t.Fatal(err)
	}
	if err := other.claimXtreamLease(t.Context(), tuner.ID, uuid.NewString()); !errors.Is(err, ErrLimitExceeded) {
		t.Fatal("stale release freed the replacement connection")
	}
	if err := other.releaseXtreamLease(t.Context(), tuner.ID, replacement); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteTuner(t.Context(), tuner.ID); err != nil {
		t.Fatal(err)
	}
	var credentials, leases int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM bloem_livetv_xtream_credentials WHERE tuner_id=$1),(SELECT count(*) FROM bloem_livetv_xtream_leases WHERE tuner_id=$1)`, tuner.ID).Scan(&credentials, &leases); err != nil || credentials != 0 || leases != 0 {
		t.Fatal("provider deletion retained secret or connection state")
	}
}

func TestBloemXtreamCreationRequiresCipherBeforeNetwork(t *testing.T) {
	service := NewServiceWithStore(&memoryStore{})
	service.xtreamTransport = xtreamRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("missing-cipher request reached the provider")
		return nil, nil
	})
	_, err := service.AddTuner(t.Context(), AddTunerInput{Type: TunerTypeXtream, URL: "https://provider.invalid", Username: "fixture", Password: "fixture"})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing cipher was not refused: %v", err)
	}
}
