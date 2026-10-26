package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// runSiloSwitchingCommand keeps a database switchable between Bloem and
// upstream Silo, or ends that. Account policy lives on users, where Silo reads
// it, so enabling only checks what Silo cannot honor and turns on the guards
// (one organization, no profile logins) and the placement of Silo's writes.
func runSiloSwitchingCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: silo silo-switching {status|enable|disable}")
	}
	command := args[0]
	flags := flag.NewFlagSet("silo-switching "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	envFile := flags.String("env", ".env", "path to .env bootstrap file")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	bc, err := config.LoadBootstrap(*envFile)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	pool, err := database.NewPool(ctx, config.DatabaseConfig{URL: bc.DatabaseURL, MaxConnections: 4})
	if err != nil {
		return fmt.Errorf("database pool: %w", err)
	}
	defer pool.Close()

	switch command {
	case "status":
	case "enable":
		report, err := tenancy.EnableSiloSwitching(ctx, pool)
		if err != nil {
			return err
		}
		fmt.Printf("silo switching enabled: memberships_created=%d roles_reconciled=%d already=%t\n",
			report.MembershipsCreated, report.RolesReconciled, report.AlreadyEnabled)
	case "disable":
		if err := tenancy.DisableSiloSwitching(ctx, pool); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown silo-switching command %q", command)
	}
	enabled, err := tenancy.SiloSwitchingEnabled(ctx, pool)
	if err != nil {
		return err
	}
	fmt.Printf("silo switching: %t\n", enabled)
	return nil
}
