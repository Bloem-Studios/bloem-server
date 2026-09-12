package notifications

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// notAccountLevelDeliveryTypes is the explicit, hand-maintained set of
// DeliveryType* constant VALUES that are deliberately NOT eligible via
// deliveryAccessPredicate's account-level fallback (delivery_access.go rule
// 4). It is kept independent of accountLevelDeliveryTypes (the production
// allowlist) on purpose: TestEveryDeliveryTypeConstantIsClassified checks
// every constant against BOTH sets, so this file is where a human records
// the "deliberately excluded" decision, not a mechanical complement of the
// production list.
//
//   - DeliveryTypeEpisodeAvailable is library-bound (its CHECK constraint
//     requires library_id, series_id and episode_id together), so it is
//     authorized by rule 2, never by the account-level fallback.
//   - DeliveryTypeRequestFulfilled is catalog-bound by construction
//     (RequestFulfillmentNotifier.NotifyFulfilled always sets SeriesID to
//     the matched item) and is authorized by rule 3. An instance reaching
//     rule 4 with no item identity is malformed data, not a legitimate
//     account-level notice, so it must stay excluded here.
var notAccountLevelDeliveryTypes = map[string]bool{
	DeliveryTypeEpisodeAvailable: true,
	DeliveryTypeRequestFulfilled: true,
}

// TestEveryDeliveryTypeConstantIsClassified parses every non-test .go file
// in this package for top-level `DeliveryType* = "..."` constant
// declarations and asserts each one's value is classified with respect to
// deliveryAccessPredicate's account-level fallback: present in either
// accountLevelDeliveryTypes (delivery_access.go, the production allowlist)
// or notAccountLevelDeliveryTypes (above, this test's explicit "deliberately
// excluded" set).
//
// The point is to make forgetting impossible to miss: a new DeliveryTypeFoo
// constant that nobody classifies fails this test by name, rather than
// silently inheriting eligibility (the original review finding) or silently
// being excluded (the allowlist fix's own residual risk). Neither silent
// outcome is acceptable — this test is the loud third option.
func TestEveryDeliveryTypeConstantIsClassified(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed; cannot locate package directory")
	}
	dir := filepath.Dir(thisFile)

	sourceFiles, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("glob package source files: %v", err)
	}
	if len(sourceFiles) == 0 {
		t.Fatalf("found no .go files in %s; the parser's directory resolution is broken", dir)
	}

	accountLevel := make(map[string]bool, len(accountLevelDeliveryTypes))
	for _, v := range accountLevelDeliveryTypes {
		accountLevel[v] = true
	}

	// name -> declared string value, collected across every file so a
	// constant declared anywhere in the package is caught regardless of
	// which file it lives in (they are not all in one file today: see the
	// task report for the exact spread).
	found := map[string]string{}
	fset := token.NewFileSet()
	for _, path := range sourceFiles {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.CONST {
				continue
			}
			for _, spec := range genDecl.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range valueSpec.Names {
					if !strings.HasPrefix(name.Name, "DeliveryType") {
						continue
					}
					if i >= len(valueSpec.Values) {
						// No value on this spec (e.g. iota continuation) —
						// not a string-literal delivery type constant.
						continue
					}
					lit, ok := valueSpec.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Fatalf(
							"%s: %s is declared but is not a plain string literal; "+
								"update this test's parser to handle it (it cannot be verified as-is)",
							filepath.Base(path), name.Name)
					}
					value, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatalf("%s: unquote %s: %v", filepath.Base(path), name.Name, err)
					}
					found[name.Name] = value
				}
			}
		}
	}

	if len(found) == 0 {
		t.Fatal("found no DeliveryType* constants; the parser is broken or the constants moved/renamed")
	}

	for name, value := range found {
		if accountLevel[value] {
			continue
		}
		if notAccountLevelDeliveryTypes[value] {
			continue
		}
		t.Errorf(
			"%s (%q) is not classified for deliveryAccessPredicate's account-level fallback: "+
				"add it to accountLevelDeliveryTypes in delivery_access.go if a delivery of this "+
				"type with no library and no item identity should be eligible for an active "+
				"organization, or to notAccountLevelDeliveryTypes in this test if it deliberately "+
				"should not be",
			name, value)
	}
}
