// Command bloem-testdb prepares a shared Go test database: migrate, finalize
// the membership policy authority, set the fixture session markers, and install
// the test-only fixture-compatibility triggers (internal/bloemtestdb).
//
// Development tooling only. It refuses a database whose name neither contains
// "test" nor ends in "_ci".
//
//	go run ./cmd/bloem-testdb                 # uses SILO_TEST_DATABASE_URL
//	go run ./cmd/bloem-testdb -recreate -database-url postgres://.../a6_test
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
)

func main() {
	dsn := flag.String("database-url", os.Getenv("SILO_TEST_DATABASE_URL"), "test database DSN (default $SILO_TEST_DATABASE_URL)")
	recreate := flag.Bool("recreate", false, "drop and recreate the database first")
	flag.Parse()
	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "bloem-testdb: no database: pass -database-url or set SILO_TEST_DATABASE_URL")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := bloemtestdb.Prepare(ctx, *dsn, bloemtestdb.Options{
		Recreate: *recreate,
		Logf: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "bloem-testdb: "+format+"\n", args...)
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
