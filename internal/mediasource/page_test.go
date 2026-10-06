package mediasource

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func pageEntry() *storagev1.Entry {
	return &storagev1.Entry{Id: "book", Name: "Å book.epub", LogicalPath: "Å book.epub", Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: 12, Revision: "v1"}
}
func TestValidatePageRejectsAmbiguousOrUnboundedDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name      string
		page      *storagev1.ListResponse
		cursor    string
		wantError bool
	}{
		{"empty terminal", &storagev1.ListResponse{Complete: true}, "", false},
		{"stalled cursor", &storagev1.ListResponse{NextCursor: "same"}, "same", true},
		{"contradictory end", &storagev1.ListResponse{Complete: true, NextCursor: "more"}, "", true},
		{"nil page", nil, "", true},
		{"missing cursor", &storagev1.ListResponse{}, "", true},
		{"advancing empty", &storagev1.ListResponse{NextCursor: "next"}, "", false},
		{"unicode name", &storagev1.ListResponse{Entries: []*storagev1.Entry{pageEntry()}, Complete: true}, "", false},
		{"duplicate identity", &storagev1.ListResponse{Entries: []*storagev1.Entry{pageEntry(), pageEntry()}, Complete: true}, "", true},
		{"cursor too long", &storagev1.ListResponse{NextCursor: strings.Repeat("x", 4097)}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePage(tc.cursor, tc.page)
			if (err != nil) != tc.wantError {
				t.Fatalf("validation: %v", err)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*storagev1.Entry)
	}{
		{"empty id", func(e *storagev1.Entry) { e.Id = "" }},
		{"empty name", func(e *storagev1.Entry) { e.Name = "" }},
		{"whitespace name", func(e *storagev1.Entry) { e.Name = "  " }},
		{"invalid utf8", func(e *storagev1.Entry) { e.Name = "\xff" }},
		{"nul id", func(e *storagev1.Entry) { e.Id = "a\x00b" }},
		{"empty revision", func(e *storagev1.Entry) { e.Revision = "" }},
		{"negative size", func(e *storagev1.Entry) { e.Size = -1 }},
		{"unknown kind", func(e *storagev1.Entry) { e.Kind = 99 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := pageEntry()
			tc.mutate(e)
			if err := ValidatePage("", &storagev1.ListResponse{Entries: []*storagev1.Entry{e}, Complete: true}); err == nil {
				t.Fatal("invalid entry accepted")
			}
		})
	}
	tooMany := &storagev1.ListResponse{Complete: true}
	for i := 0; i < 513; i++ {
		e := pageEntry()
		e.Id = fmt.Sprint(i)
		tooMany.Entries = append(tooMany.Entries, e)
	}
	if err := ValidatePage("", tooMany); err == nil {
		t.Fatal("513 entries accepted")
	}
	tooLarge := &storagev1.ListResponse{Complete: true}
	for i := 0; i < 20; i++ {
		e := pageEntry()
		e.Id = fmt.Sprint(i)
		e.LogicalPath = strings.Repeat("p", 64<<10)
		tooLarge.Entries = append(tooLarge.Entries, e)
	}
	if err := ValidatePage("", tooLarge); err == nil {
		t.Fatal("oversized encoded page accepted")
	}
	if err := ValidatePage("", &storagev1.ListResponse{Entries: []*storagev1.Entry{nil}, Complete: true}); err == nil {
		t.Fatal("nil entry accepted")
	}
}

func TestExecutablePagedTwoMillionEntryDiscovery(t *testing.T) {
	client, _, ctx := launchStorage(t)
	const wantCount = 2_000_000
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	cursor := ""
	count := 0
	pages := 0
	peak := before.HeapAlloc
	maxResponse := 0
	var encodedBytes int64
	for {
		page, err := client.List(ctx, &storagev1.ListRequest{SourceId: "scale", DirectoryId: "root", Cursor: cursor, MaxEntries: 512})
		if err != nil {
			t.Fatal(err)
		}
		if err = ValidatePage(cursor, page); err != nil {
			t.Fatal(err)
		}
		responseBytes := proto.Size(page)
		encodedBytes += int64(responseBytes)
		if responseBytes > maxResponse {
			maxResponse = responseBytes
		}
		if responseBytes > 1<<20 {
			t.Fatal("page exceeded encoded budget")
		}
		for _, entry := range page.GetEntries() {
			want := fmt.Sprintf("book-%07d", count)
			if entry.GetId() != want {
				t.Fatalf("lost or reordered entry: %s != %s", entry.GetId(), want)
			}
			count++
		}
		pages++
		var current runtime.MemStats
		runtime.ReadMemStats(&current)
		if current.HeapAlloc > peak {
			peak = current.HeapAlloc
		}
		if page.GetComplete() {
			break
		}
		cursor = page.GetNextCursor()
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if count != wantCount {
		t.Fatalf("discovered %d, want %d", count, wantCount)
	}
	if peak > 128<<20 {
		t.Fatalf("host discovery heap exceeded 128 MiB: %d", peak)
	}
	t.Logf("discovery entries=%d pages=%d elapsed=%s sampled_peak_heap_bytes=%d allocated_bytes=%d max_response_bytes=%d total_encoded_bytes=%d go=%s", count, pages, time.Since(started), peak, after.TotalAlloc-before.TotalAlloc, maxResponse, encodedBytes, runtime.Version())
	page, err := client.List(ctx, &storagev1.ListRequest{SourceId: "scale-failure", DirectoryId: "root", MaxEntries: 512})
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidatePage("", page); err != nil {
		t.Fatal(err)
	}
	_, err = client.List(ctx, &storagev1.ListRequest{SourceId: "scale-failure", DirectoryId: "root", Cursor: page.GetNextCursor(), MaxEntries: 512})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("partial failure reported as completion: %v", err)
	}
	for _, checkpoint := range []string{"512", "1000000", "1999872"} {
		request := &storagev1.ListRequest{SourceId: "scale", DirectoryId: "root", Cursor: checkpoint, MaxEntries: 128}
		first, err := client.List(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()
		_, err = client.List(cancelCtx, request)
		if status.Code(err) != codes.Canceled {
			t.Fatalf("cancel: %v", err)
		}
		replay, err := client.List(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(first, replay) {
			t.Fatal("cursor replay differs after cancellation")
		}
	}
}
