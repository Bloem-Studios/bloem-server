// Command bloem-presentation-ambience is the seasonal presentation worker.
package main

import (
	"fmt"
	"os"
	_ "time/tzdata"

	"github.com/Silo-Server/silo-server/internal/ambience"
)

func main() {
	if err := ambience.RunPlugin(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
