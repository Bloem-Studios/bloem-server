package ai

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// Test helpers Bloem's tests use, copied from Silo's tests before Silo pruned them
// (Silo #4465). Silo's test files no longer carry them.

func newTestTranscriber(client *fakeASRClient, chunks int, recordDir *string) *WhisperTranscriber {
	tr := &WhisperTranscriber{
		client:      client,
		extract:     stubExtract(evenStarts(chunks), recordDir),
		probeOffset: func(context.Context, string, int, string) float64 { return 0 },
	}
	tr.SetExtraction("", 600)
	return tr
}

// stubExtract writes fake chunk files into dir (one per start offset) and
// records dir for cleanup assertions.
func stubExtract(starts []float64, recordDir *string) func(context.Context, string, int, string, string, int) ([]playback.AudioChunk, error) {
	return func(_ context.Context, _ string, _ int, dir, _ string, _ int) ([]playback.AudioChunk, error) {
		*recordDir = dir
		var chunks []playback.AudioChunk
		for i, start := range starts {
			p := filepath.Join(dir, fmt.Sprintf("chunk%05d.wav", i))
			if err := os.WriteFile(p, []byte("RIFF"), 0o644); err != nil {
				return nil, err
			}
			chunks = append(chunks, playback.AudioChunk{Path: p, Start: start})
		}
		return chunks, nil
	}
}

func evenStarts(n int) []float64 {
	starts := make([]float64, n)
	for i := range starts {
		starts[i] = float64(i * 600)
	}
	return starts
}
