package subtitles

import "testing"

func TestProviderLanguageKeepsUnknownAndOtherProviders(t *testing.T) {
	for _, tt := range []struct{ provider, value, want string }{{"subdl", "English", "en"}, {"subdl", " Arabic ", "ar"}, {"subdl", "Unrecognized provider language", "Unrecognized provider language"}, {"upload", "English", "English"}, {"subdl", "", ""}} {
		if got := NormalizeProviderLanguage(tt.provider, tt.value); got != tt.want {
			t.Errorf("%s %q: got %q, want %q", tt.provider, tt.value, got, tt.want)
		}
	}
}
func TestStoredSubDLLanguageReadDoesNotRewriteRow(t *testing.T) {
	pool := subtitleStorageDatabase(t)
	var id int
	err := pool.QueryRow(t.Context(), `INSERT INTO downloaded_subtitles(media_file_id,provider,language,format,release_name,s3_key,score,hearing_impaired,content_sha256) VALUES (7,'subdl','English','srt','synthetic','synthetic-key',0,false,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa') RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewPgRepository(pool, nil)
	one, err := repo.GetDownloadedSubtitle(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if one.Language != "en" {
		t.Fatalf("single language = %q", one.Language)
	}
	rows, err := repo.ListDownloadedSubtitles(t.Context(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Language != "en" {
		t.Fatalf("list: %+v", rows)
	}
	matched, err := repo.GetDownloadedSubtitleByContent(t.Context(), &DownloadedSubtitle{MediaFileID: 7, Provider: "subdl", Language: "en", Format: "srt", ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err != nil || matched == nil || matched.ID != id || matched.Language != "en" {
		t.Fatalf("canonical content lookup: %+v, %v", matched, err)
	}
	var stored string
	var revision int64
	if err := pool.QueryRow(t.Context(), `SELECT language,revision FROM downloaded_subtitles WHERE id=$1`, id).Scan(&stored, &revision); err != nil {
		t.Fatal(err)
	}
	if stored != "English" || revision != one.Revision {
		t.Fatalf("read changed stored identity: %s/%d", stored, revision)
	}
}
