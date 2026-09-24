package routeinventory

import (
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// isVendoredFrontendPackage reports whether a package was pulled in from the
// frontend's dependency tree rather than written for this server.
func isVendoredFrontendPackage(pkg *packages.Package) bool {
	files := pkg.GoFiles
	if len(files) == 0 {
		files = pkg.IgnoredFiles
	}
	for _, file := range files {
		if strings.Contains(filepath.ToSlash(file), "/node_modules/") {
			return true
		}
	}
	return false
}
