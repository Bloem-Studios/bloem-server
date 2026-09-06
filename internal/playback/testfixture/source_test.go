package testfixture

import (
	"context"
	"os"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProvisionPostgresCreatesOnlyFreshSyntheticSources(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	first, err := ProvisionPostgres(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, first.AccountID); err != nil {
			t.Error(err)
		}
	}()
	second, err := ProvisionPostgres(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, second.AccountID); err != nil {
			t.Error(err)
		}
	}()
	if first.AccountID == second.AccountID || first.Source.SourceID == second.Source.SourceID || first.AdmissionID == second.AdmissionID {
		t.Fatal("existing identity reused")
	}
	for _, fixture := range []SyntheticPlaybackSource{first, second} {
		h, err := pgstore.NewPostgresProvider(pool).OpenPlaybackSink(t.Context(), fixture.Source)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.Close(); err != nil {
			t.Fatal(err)
		}
		var admitted bool
		if err := pool.QueryRow(t.Context(), `SELECT admission_id=$2::uuid AND admission_state='admitting' AND source_id=$3::uuid FROM playback_source_registrations WHERE user_id=$1`, fixture.AccountID, fixture.AdmissionID, fixture.Source.SourceID).Scan(&admitted); err != nil || !admitted {
			t.Fatalf("registration: %v %v", admitted, err)
		}
	}
}
