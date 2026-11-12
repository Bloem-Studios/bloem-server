package scanner

import (
	"path/filepath"
	"testing"
)

// Subtitle files beside a `.strm` file that share its base name are its
// external subtitles, exactly as for a local video file.
func TestStrmSidecarSubtitlesAreDetected(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Harbor Lights (2019)")
	strm := filepath.Join(dir, "Harbor Lights (2019).strm")
	writeTestFile(t, strm, "https://media.example.net/harbor.mkv\n")
	writeTestFile(t, filepath.Join(dir, "Harbor Lights (2019).en.srt"), "1\n00:00:01,000 --> 00:00:02,000\nHello\n")
	writeTestFile(t, filepath.Join(dir, "Harbor Lights (2019).nl.forced.ass"), "[Script Info]\n")
	writeTestFile(t, filepath.Join(dir, "Other Film (2020).en.srt"), "1\n00:00:01,000 --> 00:00:02,000\nNot ours\n")
	got, err := newExternalSubtitleDirCache("movies").Detect(strm)
	if err != nil {
		t.Fatal(err)
	}
	languages := map[string]string{}
	for _, sub := range got {
		languages[filepath.Base(sub.Path)] = sub.Language
	}
	if len(got) != 2 || languages["Harbor Lights (2019).en.srt"] == "" || languages["Harbor Lights (2019).nl.forced.ass"] == "" {
		t.Fatalf("sidecars of the .strm file = %+v", got)
	}
}
