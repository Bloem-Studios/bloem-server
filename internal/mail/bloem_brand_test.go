package mail

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/branding"
)

func TestBloemDefaultMailBrand(t *testing.T) {
	brand := DefaultBrand()
	if brand.Name != branding.DefaultServerName || !bytes.Equal(brand.logo.png, bloemWordmarkPNG) {
		t.Fatal("mail fallback differs from Bloem's default brand")
	}
	out := RenderLayout(LayoutOptions{BodyHTML: "<p>test</p>"})
	if !strings.Contains(out, `alt="Bloem"`) || !strings.Contains(out, `src="cid:silo-logo"`) {
		t.Fatal("email lost the branded inline logo")
	}
}
