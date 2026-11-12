package procgroup

import (
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func TestNativeLocationMatchesStorageSource(t *testing.T) {
	for _, location := range []string{
		"/media/Movies/Harbor Lights (2019).strm", "/media/a.STRM", "bloem-storage:v1:abc", "bloem-storage:",
		"/media/a.mkv", "/media/bloem-storage:a.mkv", "/media/a.strm.srt", "", "https://example.net/a.strm",
	} {
		if got, want := NativeLocation(location), storagesource.IsNativeLocation(location); got != want {
			t.Errorf("NativeLocation(%q) = %v, storagesource says %v", location, got, want)
		}
	}
}

func TestCommandContextRefusesNativeInputs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		refused bool
	}{
		{"ffmpeg input", []string{"-v", "error", "-i", "/media/a.strm", "-map", "0:s:0", "-f", "srt", "file:/tmp/out.srt"}, true},
		{"file URL input", []string{"-i", "FILE:/media/a.strm", "-f", "null", "-"}, true},
		{"storage identity", []string{"-i", "bloem-storage:v1:abc", "-f", "null", "-"}, true},
		{"ffprobe trailing input", []string{"-v", "error", "-of", "json", "/media/a.strm"}, true},
		{"second input", []string{"-i", "/media/a.mkv", "-i", "/media/a.strm", "-f", "null", "-"}, true},
		{"ordinary file", []string{"-v", "error", "-i", "/media/a.mkv", "-f", "null", "-"}, false},
		{"ordinary ffprobe", []string{"-v", "error", "-of", "json", "/media/a.mkv"}, false},
		{"name elsewhere", []string{"-i", "/media/a.mkv", "-metadata", "title=a.strm", "-f", "null", "-"}, false},
		{"private source address", []string{"-i", "http://127.0.0.1:4100/source", "-f", "null", "-"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CommandContext(t.Context(), "true", tc.args...).Run()
			if refused := errors.Is(err, ErrNativeInput); refused != tc.refused || (!tc.refused && err != nil) {
				t.Fatalf("Run() = %v, refused want %v", err, tc.refused)
			}
		})
	}
}
