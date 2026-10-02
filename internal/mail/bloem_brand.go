package mail

import _ "embed"

// Keep the email fallback aligned with Bloem's bundled sidebar wordmark.
// The upstream MIME content ID stays unchanged for compatibility.
//
//go:embed assets/bloem-wordmark.png
var bloemWordmarkPNG []byte

func init() {
	defaultWordmarkPNG = bloemWordmarkPNG
	defaultLogo = mustDefaultLogo()
}
