// Package clientv1 embeds the client DTO registry so cmd/clientdtogen carries
// the schema it validates registries against.
//
// registry.json is the single contract source for the generated client DTOs
// (docs/specs/client-dto-generator.md §3): it lists, per Go package, the root
// wire types the generator walks transitively, with their direction, dialect
// and capability gate. registry.schema.json documents and enforces its shape.
// Loading and semantic validation live in cmd/clientdtogen/internal/registry.
package clientv1

import "embed"

// FS holds registry.json and registry.schema.json.
//
//go:embed registry.json registry.schema.json
var FS embed.FS
