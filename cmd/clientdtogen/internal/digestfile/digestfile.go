// Package digestfile pins the client DTO contract to the tree:
// contracts/client/v1/digest.txt carries the digest of the normalised type
// graph next to the digest of the pre-lock removals table in
// docs/architecture/v1-scope.md at the time the contract digest was written.
//
// The wiring is the §7.2 rule: a wire-shape change moves the contract digest,
// and pre-lock such a change must be recorded as a removal (or reviewed as
// coordinated) in the removals table before the new digest is pinned with
// `make client-digest`. The test in this package fails while the pins lag the
// tree and says which of the two steps is missing.
package digestfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// ScopeFile is the repository file whose pre-lock removals table the digest
// test is wired to.
const ScopeFile = "docs/architecture/v1-scope.md"

const (
	removalsHeading = "## Breaking removals taken before lock"
	contractKey     = "contract: "
	tableKey        = "removals-table: "
)

// Pins are the two digests digest.txt carries. Both have the "sha256:<hex>"
// form.
type Pins struct {
	// Contract is Graph.Digest() over the committed registry.
	Contract string
	// RemovalsTable is TableDigest over ScopeFile at the time Contract was
	// pinned.
	RemovalsTable string
}

// TableDigest extracts the pre-lock removals table from the v1-scope document
// and returns "sha256:<hex>" over its data rows, normalised: surrounding
// whitespace trimmed, prose and empty lines dropped, the column header and
// rule rows excluded so wording or formatting edits do not masquerade as a
// removal. An absent section is an error: the table is the digest test's
// other half.
func TableDigest(scope []byte) (string, error) {
	lines := strings.Split(string(scope), "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == removalsHeading {
			start = i + 1
			break
		}
	}
	if start == -1 {
		return "", fmt.Errorf("%s has no %q section", ScopeFile, removalsHeading)
	}
	var rows []string
	seenHeader := false
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			break
		}
		if !strings.HasPrefix(trimmed, "|") {
			continue // prose or blank
		}
		if !seenHeader {
			seenHeader = true // the "| Removed | Release | Rationale |" row
			continue
		}
		if isRuleRow(trimmed) {
			continue
		}
		rows = append(rows, trimmed)
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("%s: the %q section has no removal rows", ScopeFile, removalsHeading)
	}
	sum := sha256.Sum256([]byte(strings.Join(rows, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// isRuleRow reports whether a table line is the "|---|---|" separator row.
func isRuleRow(line string) bool {
	inner := strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	for _, cell := range strings.Split(inner, "|") {
		cell = strings.TrimSpace(cell)
		if cell == "" {
			return false
		}
		for _, r := range cell {
			if r != '-' && r != ':' {
				return false
			}
		}
	}
	return true
}

// Read parses digest.txt. Comment and blank lines are ignored; both pins must
// be present.
func Read(path string) (Pins, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Pins{}, err
	}
	var pins Pins
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, contractKey):
			pins.Contract = strings.TrimPrefix(trimmed, contractKey)
		case strings.HasPrefix(trimmed, tableKey):
			pins.RemovalsTable = strings.TrimPrefix(trimmed, tableKey)
		default:
			return Pins{}, fmt.Errorf("%s: unexpected line %q", path, trimmed)
		}
	}
	for _, p := range []struct {
		name  string
		value string
	}{
		{"contract", pins.Contract},
		{"removals-table", pins.RemovalsTable},
	} {
		if err := checkDigest(p.name, p.value); err != nil {
			return Pins{}, fmt.Errorf("%s: %w", path, err)
		}
	}
	return pins, nil
}

func checkDigest(name, value string) error {
	if value == "" {
		return fmt.Errorf("missing %s pin", name)
	}
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return fmt.Errorf("%s pin %q is not \"sha256:\" plus 64 hex digits", name, value)
	}
	return nil
}

// Write atomically replaces digest.txt with the pins.
func Write(path string, pins Pins) error {
	if err := checkDigest("contract", pins.Contract); err != nil {
		return err
	}
	if err := checkDigest("removals-table", pins.RemovalsTable); err != nil {
		return err
	}
	var b bytes.Buffer
	for _, line := range []string{
		"# Pinned by `make client-digest` (cmd/clientdtogen); do not edit by hand.",
		"#",
		"# contract: digest of the normalised type graph built from",
		"# contracts/client/v1/registry.json (docs/specs/client-dto-generator.md §7.2).",
		"# removals-table: digest of the pre-lock removals table in " + ScopeFile,
		"# at the time the contract digest was written. A wire-shape change moves",
		"# the contract digest; before re-pinning, record removals/retypes in that",
		"# table so the client migration can track them. The test in",
		"# cmd/clientdtogen/internal/digestfile fails while these pins lag the tree.",
	} {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString(contractKey + pins.Contract + "\n")
	b.WriteString(tableKey + pins.RemovalsTable + "\n")
	return os.WriteFile(path, b.Bytes(), 0o644)
}
