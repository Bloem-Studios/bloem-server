package remotesource

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Stat asks for one byte and learns the remote size and validators, so a
// rescan can tell an unchanged remote file without reading its media.
func TestStatReadsValidatorsWithOneByte(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 3<<20)
	var mu sync.Mutex
	var ranges []string
	etag := `"v1"`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		w.Header().Set("ETag", etag)
		mu.Unlock()
		http.ServeContent(w, r, "remote.mkv", time.Unix(1_700_000_000, 0), bytes.NewReader(data))
	}))
	t.Cleanup(server.Close)
	guard := NewGuard(Options{AllowPrivate: func(context.Context) bool { return true }})

	got, err := Stat(t.Context(), guard.Client(), server.URL+"/remote.mkv?token=x")
	if err != nil {
		t.Fatal(err)
	}
	want := Validators{Size: int64(len(data)), ETag: `"v1"`, LastModified: time.Unix(1_700_000_000, 0).UTC()}
	if got != want {
		t.Fatalf("Stat = %+v, want %+v", got, want)
	}
	if len(ranges) != 1 || ranges[0] != "bytes=0-0" {
		t.Fatalf("Stat requests = %q, want one bytes=0-0", ranges)
	}

	file, err := Open(t.Context(), guard.Client(), server.URL+"/remote.mkv", "remote.mkv")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if file.Validators() != want {
		t.Fatalf("File.Validators = %+v, want %+v", file.Validators(), want)
	}

	// By default the guard refuses the loopback server, also for Stat.
	strict := NewGuard(Options{})
	if _, err := Stat(t.Context(), strict.Client(), server.URL+"/remote.mkv"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("Stat through a refusing guard = %v", err)
	}
}

// A server that ignores ranges answers 200; Stat takes the length from the
// headers and does not read the body.
func TestStatWithoutRangesUsesContentLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "5000000")
		w.Header().Set("Last-Modified", time.Unix(1_600_000_000, 0).UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, 64<<10))
	}))
	t.Cleanup(server.Close)
	guard := NewGuard(Options{AllowPrivate: func(context.Context) bool { return true }})
	got, err := Stat(t.Context(), guard.Client(), server.URL+"/x")
	if err != nil || got.Size != 5000000 || got.ETag != "" || !got.LastModified.Equal(time.Unix(1_600_000_000, 0)) {
		t.Fatalf("Stat = %+v, %v", got, err)
	}
}

// Unchanged needs the same size and a validator that can tell: the ETag when
// one was recorded, otherwise Last-Modified. With neither, it cannot tell.
func TestValidatorsUnchanged(t *testing.T) {
	when := time.Unix(1_700_000_000, 0).UTC()
	for _, tc := range []struct {
		name          string
		stored, fresh Validators
		want          bool
	}{
		{"same etag", Validators{Size: 10, ETag: `"a"`}, Validators{Size: 10, ETag: `"a"`, LastModified: when}, true},
		{"etag changed", Validators{Size: 10, ETag: `"a"`, LastModified: when}, Validators{Size: 10, ETag: `"b"`, LastModified: when}, false},
		{"etag gone", Validators{Size: 10, ETag: `"a"`, LastModified: when}, Validators{Size: 10, LastModified: when}, false},
		{"size changed", Validators{Size: 10, ETag: `"a"`}, Validators{Size: 11, ETag: `"a"`}, false},
		{"same last-modified", Validators{Size: 10, LastModified: when}, Validators{Size: 10, LastModified: when}, true},
		{"last-modified changed", Validators{Size: 10, LastModified: when}, Validators{Size: 10, LastModified: when.Add(time.Second)}, false},
		{"no validators", Validators{Size: 10}, Validators{Size: 10}, false},
		{"weak etag", Validators{Size: 10, ETag: `W/"a"`}, Validators{Size: 10, ETag: `W/"a"`}, true},
	} {
		if got := tc.stored.Unchanged(tc.fresh); got != tc.want {
			t.Errorf("%s: Unchanged = %v, want %v", tc.name, got, tc.want)
		}
	}
}
