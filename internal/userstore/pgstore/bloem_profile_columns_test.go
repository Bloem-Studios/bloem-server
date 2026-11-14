package pgstore

// Bloem-owned test: Bloem's copies of Silo's profile queries
// (bloem_profiles.go, bloem_restored_profiles.go) keep every column Silo's
// originals in profiles.go read or write.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Bloem writes and lists profiles with its own copies of Silo's queries,
// because its rows also carry an organization and an access group. A column
// Silo adds to its query and Bloem's copy lacks is lost on Bloem's path, or,
// for a list read through Silo's scanProfile, breaks every scan. Silo #2612
// added requests_blocked this way.
func TestBloemProfileQueriesKeepSiloColumns(t *testing.T) {
	silo := readSource(t, "profiles.go")
	bloem := readSource(t, "bloem_profiles.go")
	restored := readSource(t, "bloem_restored_profiles.go")

	cases := []struct {
		name        string
		siloColumns []string
		bloemCopy   []string
	}{
		{
			name:        "createProfile insert",
			siloColumns: insertColumns(t, functionSource(t, silo, "func createProfile(")),
			bloemCopy:   insertColumns(t, functionSource(t, bloem, "func bloemCreateProfile(")),
		},
		{
			name:        "ListProfiles select",
			siloColumns: selectColumns(t, functionSource(t, silo, "func (s *PostgresUserStore) ListProfiles(")),
			bloemCopy:   selectColumns(t, functionSource(t, bloem, "func bloemListProfiles(")),
		},
		{
			name:        "CreateRestoredProfileInTx insert",
			siloColumns: insertColumns(t, functionSource(t, silo, "func CreateRestoredProfileInTx(")),
			bloemCopy:   insertColumns(t, functionSource(t, restored, "func bloemCreateRestoredProfileInTx(")),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			have := make(map[string]bool, len(tc.bloemCopy))
			for _, column := range tc.bloemCopy {
				have[column] = true
			}
			for _, column := range tc.siloColumns {
				if !have[column] {
					t.Errorf("Bloem's copy lacks Silo's column %q", column)
				}
			}
		})
	}

	// A list read goes through Silo's scanProfile, which scans its columns in
	// order, so Bloem's select must list Silo's columns first and in order.
	siloList := selectColumns(t, functionSource(t, silo, "func (s *PostgresUserStore) ListProfiles("))
	bloemList := selectColumns(t, functionSource(t, bloem, "func bloemListProfiles("))
	if len(bloemList) < len(siloList) || strings.Join(bloemList[:len(siloList)], ",") != strings.Join(siloList, ",") {
		t.Errorf("bloemListProfiles selects %v first, Silo's ListProfiles %v", bloemList, siloList)
	}
}

func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// functionSource returns the source from the line that starts with signature
// to the next top-level declaration.
func functionSource(t *testing.T, src, signature string) string {
	t.Helper()
	at := strings.Index(src, "\n"+signature)
	if at < 0 {
		t.Fatalf("no %q in source", signature)
	}
	body := src[at+1:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}
	return body
}

var (
	insertColumnsRe = regexp.MustCompile(`(?s)INSERT INTO user_profiles \((.*?)\)\s*VALUES`)
	selectColumnsRe = regexp.MustCompile(`(?s)SELECT (.*?)\s+FROM user_profiles`)
	coalesceRe      = regexp.MustCompile(`COALESCE\((\w+), [^)]*\)`)
)

func insertColumns(t *testing.T, src string) []string {
	t.Helper()
	m := insertColumnsRe.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("no INSERT INTO user_profiles in:\n%s", src)
	}
	return columnList(m[1])
}

func selectColumns(t *testing.T, src string) []string {
	t.Helper()
	m := selectColumnsRe.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("no SELECT ... FROM user_profiles in:\n%s", src)
	}
	return columnList(m[1])
}

func columnList(list string) []string {
	list = coalesceRe.ReplaceAllString(list, "$1")
	var columns []string
	for _, column := range strings.Split(list, ",") {
		column = strings.TrimSpace(column)
		// Bloem's list appends its own columns through bloemProfileColumns.
		if column == "" || strings.Contains(column, "bloemProfileColumns") {
			continue
		}
		columns = append(columns, column)
	}
	return columns
}
