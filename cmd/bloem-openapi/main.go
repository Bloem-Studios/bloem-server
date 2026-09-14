// Command bloem-openapi writes the Bloem native API OpenAPI artifact from the
// Go registries alone. Like cmd/apiv2-openapi it opens no database, network or
// credential and reads nothing from the environment, so two runs on any machine
// produce the same bytes; make verify-bloem-openapi relies on that.
//
// Usage:
//
//	bloem-openapi -out contracts/api/bloem/v1/openapi.json
//	bloem-openapi -check contracts/api/bloem/v1/openapi.json
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/Silo-Server/silo-server/internal/apiv2"
)

func main() {
	out := flag.String("out", "", "write the generated document to this path")
	check := flag.String("check", "", "exit 1 unless this file equals the generated document byte for byte")
	flag.Parse()
	if (*out == "") == (*check == "") {
		fmt.Fprintln(os.Stderr, "usage: bloem-openapi (-out PATH | -check PATH)")
		os.Exit(2)
	}

	doc, err := apiv2.GenerateBloemOpenAPI()
	if err != nil {
		fmt.Fprintln(os.Stderr, "bloem-openapi:", err)
		os.Exit(1)
	}

	if *out != "" {
		if err := os.WriteFile(*out, doc, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "bloem-openapi:", err)
			os.Exit(1)
		}
		return
	}

	committed, err := os.ReadFile(*check)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bloem-openapi:", err)
		os.Exit(1)
	}
	if !bytes.Equal(committed, doc) {
		fmt.Fprintf(os.Stderr,
			"bloem-openapi: %s is stale; regenerate it with `make bloem-openapi`\n", *check)
		os.Exit(1)
	}
}
