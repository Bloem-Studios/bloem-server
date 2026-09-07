// playback-source-admission inspects or explicitly applies one retained first
// admission intent. It never starts playback, creates accounts, or migrates DBs.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	intentPath := flag.String("intent", "", "existing private JSON file containing the retained exact first-admission intent")
	apply := flag.Bool("apply", false, "commit first admission for this one account (default is read-only plan)")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, *intentPath, *apply, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func readIntent(r io.Reader) (pgstore.FirstAdmissionIntent, error) {
	var intent pgstore.FirstAdmissionIntent
	raw, err := io.ReadAll(io.LimitReader(r, 16385))
	if err != nil {
		return intent, err
	}
	if len(raw) > 16384 {
		return intent, errors.New("intent exceeds 16 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&intent); err != nil {
		return intent, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return intent, errors.New("intent must contain exactly one JSON object")
	}
	return intent, intent.Validate()
}

func run(ctx context.Context, path string, apply bool, out io.Writer) error {
	if path == "" {
		return errors.New("--intent is required; retain the same file across plan, apply and uncertain-result inspection")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	intent, err := readIntent(f)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	dsn := os.Getenv("SILO_ADMISSION_DATABASE_URL")
	if dsn == "" {
		return errors.New("SILO_ADMISSION_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errors.New("invalid admission database configuration")
	}
	defer pool.Close()
	decision, err := pgstore.NewPostgresProvider(pool).FirstAdmission(ctx, intent, apply)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(decision)
}
