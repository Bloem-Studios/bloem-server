package branding

import (
	"regexp"
)

// indexTitleTag matches whatever title the shell was built with rather than a
// specific product name. The web build rewrites the bundled "Silo" title to the
// Bloem product brand, so a literal match here silently stopped working once —
// and would break again on any rebrand or upstream shell change.
var indexTitleTag = regexp.MustCompile(`(?i)<title>[^<]*</title>`)
