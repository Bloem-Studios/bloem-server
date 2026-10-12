package storagesource

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAudioPresentationKeyIsStableAndScoped(t *testing.T) {
	location := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	first, err := AudioPresentationKey(location, "book")
	if err != nil || !strings.HasPrefix(first, "bloem-storage:audiobook:") || len(first) != 88 {
		t.Fatalf("key=%q err=%v", first, err)
	}
	again, _ := AudioPresentationKey(location, "book")
	other, _ := AudioPresentationKey(uuid.New(), "book")
	dir, _ := AudioPresentationKey(location, "other")
	if first != again || first == other || first == dir || !IsNativeAudioPresentationKey(first) {
		t.Fatal("presentation identity is not stable and scoped")
	}
	for _, key := range []string{"", "bloem-storage:audiobook:", first + "0", strings.ToUpper(first)} {
		if IsNativeAudioPresentationKey(key) {
			t.Fatalf("malformed key admitted: %q", key)
		}
	}
	if _, err := AudioPresentationKey(uuid.Nil, "book"); err == nil {
		t.Fatal("nil location admitted")
	}
	if _, err := AudioPresentationKey(location, ""); err == nil {
		t.Fatal("empty directory admitted")
	}
}
