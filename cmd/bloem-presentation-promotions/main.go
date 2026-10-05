package main

import (
	"fmt"
	"os"

	"github.com/Silo-Server/silo-server/internal/promotions"
)

func main() {
	if err := promotions.RunPlugin(os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
