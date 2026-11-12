package mediasample

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/Silo-Server/silo-server/internal/procgroup"
)

// A trickplay sheet or an intro fingerprint of a `.strm` item would hand
// ffmpeg a concat list naming the text file. Run refuses it before ffmpeg
// starts, and callers read the refusal as permanent, as they did ffmpeg's
// own invalid-data failure on the text.
func TestRunRefusesNativeInputsWithoutStartingFFmpeg(t *testing.T) {
	runner := Runner{Exec: fakeExec(func(context.Context, []string, io.Writer, io.Writer) error {
		t.Fatal("ffmpeg started for a native catalog location")
		return nil
	})}
	for _, input := range []string{"/media/Harbor Lights (2019).strm", "bloem-storage:v1:abc"} {
		_, err := runner.Run(t.Context(), Request{Input: input, Samples: &Samples{Seconds: []float64{10, 20}}, Sheets: &SheetsOutput{TileWidth: 160, TileHeight: 90, Columns: 2, Rows: 1, Quality: 5}})
		if !errors.Is(err, procgroup.ErrNativeInput) || !Classify(err).Permanent() {
			t.Fatalf("%s: err = %v (%s), want a permanent native-input refusal", input, err, Classify(err))
		}
	}
}
