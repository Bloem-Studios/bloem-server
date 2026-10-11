package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestInvalidGrantArgumentsCannotExposeConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "private-database-value")
	t.Setenv("SECRET_KEY", "private-key")
	for _, args := range [][]string{{"-installation", "-1"}, {"-installation", "1", "-action", "activate"}, {"-installation", "1", "unexpected"}} {
		var out bytes.Buffer
		err := run(context.Background(), args, &out)
		if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "private") {
			t.Fatal("invalid command leaked config or succeeded")
		}
	}
}
