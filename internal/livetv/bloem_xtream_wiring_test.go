package livetv

import (
	"bytes"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/secret"
)

func TestBloemXtreamCipherPublication(t *testing.T) {
	service := NewServiceWithStore(&PgStore{})
	if _, err := service.xtreamStore(); !errors.Is(err, ErrNotConfigured) {
		t.Fatal("missing cipher must fail closed")
	}
	cipher, err := secret.New(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	service.SetXtreamCipher(cipher)
	done := make(chan struct{})
	t.Cleanup(func() { <-done })
	go func() {
		defer close(done)
		for range 1000 {
			// API router wiring repeats the already configured key while
			// the task manager may be admitting a guide or recording.
			service.SetXtreamCipher(cipher)
		}
	}()
	for range 1000 {
		if _, err := service.xtreamStore(); err != nil {
			t.Fatal(err)
		}
	}
	<-done
	service.SetXtreamCipher(nil)
	if _, err := service.xtreamStore(); !errors.Is(err, ErrNotConfigured) {
		t.Fatal("clearing the cipher must fail closed")
	}
}
