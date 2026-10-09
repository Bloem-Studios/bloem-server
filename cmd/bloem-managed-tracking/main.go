// Command bloem-managed-tracking manages backend-only enrollment grants.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"os"
	"time"
)

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("bloem-managed-tracking", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	action := flags.String("action", "info", "grant, info, rotate or revoke")
	id := flags.Int("installation", 0, "installed Pastime plugin ID")
	tenant := flags.String("tenant", "", "tenant UUID for grant")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *id < 1 || (*action != "grant" && *action != "info" && *action != "rotate" && *action != "revoke") {
		return errors.New("invalid managed-tracking command arguments")
	}
	cipher, err := secret.New([]byte(os.Getenv("SECRET_KEY")))
	if err != nil {
		return errors.New("managed-tracking encryption is unavailable")
	}
	if os.Getenv("DATABASE_URL") == "" {
		return errors.New("managed-tracking database is unavailable")
	}
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return errors.New("managed-tracking database is unavailable")
	}
	defer pool.Close()
	s := &managedtracking.Service{Pool: pool, Cipher: cipher}
	switch *action {
	case "grant":
		err = s.Grant(ctx, *id, *tenant)
	case "rotate":
		err = s.Rotate(ctx, *id)
	case "revoke":
		err = s.Revoke(ctx, *id)
	}
	if err != nil {
		return errors.New("managed-tracking operation rejected")
	}
	if *action == "revoke" {
		_, err = fmt.Fprintln(out, "Managed tracking revoked.")
		return err
	}
	info, err := s.GetInfo(ctx, *id)
	if err != nil {
		return errors.New("managed-tracking authority unavailable")
	}
	return json.NewEncoder(out).Encode(struct {
		InstanceID string `json:"instance_id"`
		Scope      string `json:"scope"`
		Generation uint64 `json:"credential_generation"`
	}{info.InstanceID, info.Scope, info.Generation})
}
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
