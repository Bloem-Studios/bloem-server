package mediasource

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func audioEntry() *storagev1.Entry {
	entry := pageEntry()
	entry.Name = "book.m4b"
	entry.Audiobook = &storagev1.AudiobookMetadata{BookId: "book", Title: "Book", Authors: []string{"Alice"}, Narrators: []string{"Bob"}, Genres: []string{"Fiction"}}
	entry.AudioFile = &storagev1.AudioFileMetadata{BookId: "book", DurationSeconds: 10, Codec: "aac", Container: "m4b", Chapters: []*storagev1.AudioChapter{{Id: "chapter", Title: "One", StartSeconds: 0, EndSeconds: 10}}}
	return entry
}

func TestValidatePageRejectsMalformedAudioMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*storagev1.Entry)
	}{
		{"nan duration", func(e *storagev1.Entry) { e.AudioFile.DurationSeconds = math.NaN() }},
		{"infinite duration", func(e *storagev1.Entry) { e.AudioFile.DurationSeconds = math.Inf(1) }},
		{"negative duration", func(e *storagev1.Entry) { e.AudioFile.DurationSeconds = -1 }},
		{"nan start", func(e *storagev1.Entry) { e.AudioFile.Chapters[0].StartSeconds = math.NaN() }},
		{"nan end", func(e *storagev1.Entry) { e.AudioFile.Chapters[0].EndSeconds = math.NaN() }},
		{"infinite start", func(e *storagev1.Entry) { e.AudioFile.Chapters[0].StartSeconds = math.Inf(-1) }},
		{"infinite end", func(e *storagev1.Entry) { e.AudioFile.Chapters[0].EndSeconds = math.Inf(1) }},
		{"negative start", func(e *storagev1.Entry) { e.AudioFile.Chapters[0].StartSeconds = -1 }},
		{"negative end", func(e *storagev1.Entry) { e.AudioFile.Chapters[0].EndSeconds = -1 }},
		{"end before start", func(e *storagev1.Entry) {
			e.AudioFile.Chapters[0].StartSeconds = 9
			e.AudioFile.Chapters[0].EndSeconds = 8
		}},
		{"chapter past duration", func(e *storagev1.Entry) { e.AudioFile.Chapters[0].EndSeconds = 11 }},
		{"duplicate chapters", func(e *storagev1.Entry) { e.AudioFile.Chapters = append(e.AudioFile.Chapters, e.AudioFile.Chapters[0]) }},
		{"empty chapter id", func(e *storagev1.Entry) { e.AudioFile.Chapters[0].Id = "" }},
		{"nil chapter", func(e *storagev1.Entry) { e.AudioFile.Chapters[0] = nil }},
		{"too many chapters", func(e *storagev1.Entry) { e.AudioFile.Chapters = audioChapters(1025) }},
		{"book title too long", func(e *storagev1.Entry) { e.Audiobook.Title = strings.Repeat("x", 4097) }},
		{"description too long", func(e *storagev1.Entry) { e.Audiobook.Description = strings.Repeat("x", (64<<10)+1) }},
		{"too many authors", func(e *storagev1.Entry) { e.Audiobook.Authors = make([]string, 65) }},
		{"too many narrators", func(e *storagev1.Entry) { e.Audiobook.Narrators = make([]string, 65) }},
		{"too many genres", func(e *storagev1.Entry) { e.Audiobook.Genres = make([]string, 65) }},
		{"author too long", func(e *storagev1.Entry) { e.Audiobook.Authors[0] = strings.Repeat("x", 4097) }},
		{"narrator nul", func(e *storagev1.Entry) { e.Audiobook.Narrators[0] = "a\x00b" }},
		{"genre invalid utf8", func(e *storagev1.Entry) { e.Audiobook.Genres[0] = "\xff" }},
		{"chapter title too long", func(e *storagev1.Entry) { e.AudioFile.Chapters[0].Title = strings.Repeat("x", 4097) }},
		{"chapter id too long", func(e *storagev1.Entry) { e.AudioFile.Chapters[0].Id = strings.Repeat("x", 4097) }},
		{"codec too long", func(e *storagev1.Entry) { e.AudioFile.Codec = strings.Repeat("x", 4097) }},
		{"container invalid utf8", func(e *storagev1.Entry) { e.AudioFile.Container = "\xff" }},
		{"file book id too long", func(e *storagev1.Entry) { e.AudioFile.BookId = strings.Repeat("x", 4097) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := audioEntry()
			tc.mutate(entry)
			if err := ValidatePage("", &storagev1.ListResponse{Entries: []*storagev1.Entry{entry}, Complete: true}); err == nil {
				t.Fatal("malformed audio metadata accepted")
			}
		})
	}
}

func audioChapters(count int) []*storagev1.AudioChapter {
	chapters := make([]*storagev1.AudioChapter, count)
	for i := range chapters {
		chapters[i] = &storagev1.AudioChapter{Id: fmt.Sprint(i), EndSeconds: 10}
	}
	return chapters
}

func TestValidatePageAcceptsBoundedAudioWithoutGrantingRevision(t *testing.T) {
	entry := audioEntry()
	entry.Audiobook.Description = strings.Repeat("x", 64<<10)
	entry.Audiobook.Title = strings.Repeat("x", 4096)
	entry.Audiobook.Authors = make([]string, 64)
	entry.Audiobook.Narrators = make([]string, 64)
	entry.Audiobook.Genres = make([]string, 64)
	entry.AudioFile.Chapters = audioChapters(1024)
	page := &storagev1.ListResponse{Entries: []*storagev1.Entry{entry}, Complete: true}
	if err := ValidatePage("", page); err != nil {
		t.Fatal(err)
	}
	entry.Revision = ""
	if err := ValidatePage("", page); err == nil {
		t.Fatal("audio metadata granted a revision")
	}
}

func TestValidatePageRetainsOneMiBWithAudioMetadata(t *testing.T) {
	page := &storagev1.ListResponse{Complete: true}
	for i := 0; i < 16; i++ {
		entry := audioEntry()
		entry.Id = fmt.Sprint(i)
		entry.Audiobook.Description = strings.Repeat("x", 64<<10)
		page.Entries = append(page.Entries, entry)
	}
	if err := ValidatePage("", page); err == nil {
		t.Fatal("oversized audio page accepted")
	}
}

type audioStatProvider struct {
	storagev1.UnimplementedStorageProviderServer
	entry *storagev1.Entry
}

func (p audioStatProvider) Stat(context.Context, *storagev1.StatRequest) (*storagev1.StatResponse, error) {
	return &storagev1.StatResponse{Entry: p.entry}, nil
}
func TestPluginStatValidatesBoundedAudioMetadata(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutate    func(*storagev1.Entry)
		wantError bool
	}{
		{"valid bounded chapters", func(e *storagev1.Entry) {
			e.Audiobook.Description = strings.Repeat("x", 64<<10)
			e.AudioFile.Chapters = audioChapters(1024)
		}, false},
		{"nan duration", func(e *storagev1.Entry) { e.AudioFile.DurationSeconds = math.NaN() }, true},
		{"unbounded title", func(e *storagev1.Entry) { e.Audiobook.Title = strings.Repeat("x", 4097) }, true},
		{"audio cannot grant revision", func(e *storagev1.Entry) { e.Revision = "" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := audioEntry()
			tc.mutate(entry)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			source := NewPluginSource(rpcClient(t, audioStatProvider{entry: entry}))
			_, err := source.Stat(ctx, Ref{SourceID: "source", EntryID: entry.Id, Revision: "v1"})
			if (err != nil) != tc.wantError {
				t.Fatalf("Stat validation: %v", err)
			}
		})
	}
}

// Bypass transport limits to exercise the adapter's own wire-size boundary.
type directAudioStatClient struct {
	storagev1.StorageProviderClient
	entry *storagev1.Entry
}

func (c directAudioStatClient) Stat(context.Context, *storagev1.StatRequest, ...grpc.CallOption) (*storagev1.StatResponse, error) {
	return &storagev1.StatResponse{Entry: c.entry}, nil
}
func TestPluginStatRejectsOversizedAudioWithoutTransportLimit(t *testing.T) {
	entry := audioEntry()
	entry.AudioFile.Chapters = audioChapters(1024)
	for _, chapter := range entry.AudioFile.Chapters {
		chapter.Title = strings.Repeat("x", 1024)
	}
	source := NewPluginSource(directAudioStatClient{entry: entry})
	if _, err := source.Stat(context.Background(), Ref{SourceID: "source", EntryID: entry.Id, Revision: "v1"}); err == nil {
		t.Fatal("oversized Stat accepted without transport limit")
	}
}

func TestValidatePageBoundsEveryAudioTextField(t *testing.T) {
	for _, tc := range []struct {
		name          string
		fields        []protoreflect.Name
		selectMessage func(*storagev1.Entry) protoreflect.Message
	}{
		{"book", []protoreflect.Name{"book_id", "title", "publisher", "published_date", "language", "isbn", "series", "series_index", "cover_entry_id", "cover_revision", "cover_thumbhash"}, func(e *storagev1.Entry) protoreflect.Message { return e.Audiobook.ProtoReflect() }},
		{"track", []protoreflect.Name{"book_id", "codec", "container"}, func(e *storagev1.Entry) protoreflect.Message { return e.AudioFile.ProtoReflect() }},
		{"chapter", []protoreflect.Name{"id", "title"}, func(e *storagev1.Entry) protoreflect.Message { return e.AudioFile.Chapters[0].ProtoReflect() }},
	} {
		for _, name := range tc.fields {
			t.Run(tc.name+"/"+string(name), func(t *testing.T) {
				for _, invalid := range []string{strings.Repeat("x", 4097), "a\x00b", "\xff"} {
					entry := audioEntry()
					message := tc.selectMessage(entry)
					message.Set(message.Descriptor().Fields().ByName(name), protoreflect.ValueOfString(invalid))
					if err := ValidatePage("", &storagev1.ListResponse{Entries: []*storagev1.Entry{entry}, Complete: true}); err == nil {
						t.Fatal("malformed audio text accepted")
					}
				}
			})
		}
	}
}
