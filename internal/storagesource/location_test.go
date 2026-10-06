package storagesource

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCatalogLocation(t *testing.T) {
	b := uuid.New()
	other := uuid.New()
	first, err := CatalogLocation(b, "opaque/../book")
	if err != nil {
		t.Fatal(err)
	}
	same, err := CatalogLocation(b, "opaque/../book")
	if err != nil || same != first {
		t.Fatal("unstable location")
	}
	different, err := CatalogLocation(other, "opaque/../book")
	if err != nil || different == first {
		t.Fatal("cross-library collision")
	}
	if !strings.HasPrefix(first, "bloem-storage:") || strings.Contains(first, "opaque") {
		t.Fatal("location exposes a path")
	}
	for _, id := range []string{"", strings.Repeat("a", 1025), "nul\x00", string([]byte{255})} {
		if _, err := CatalogLocation(b, id); err == nil {
			t.Fatal("invalid entry accepted")
		}
	}
	if _, err := CatalogLocation(uuid.Nil, "book"); err == nil {
		t.Fatal("empty binding accepted")
	}
}
