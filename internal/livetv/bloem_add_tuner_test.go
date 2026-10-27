package livetv

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/livetv/hdhomerun"
)

// A device that answers discovery but not its lineup is saved with the scan
// failure on it, and adding it again updates the same tuner.
func TestAddTunerAnswersWithTheTunerWhenItsFirstScanFails(t *testing.T) {
	store := newMemoryStore()
	svc := NewServiceWithStore(store)
	svc.SetHDHomeRunClient(fakeHDHRClient{
		discover: func(context.Context, string, string) (*hdhomerun.DeviceInfo, error) {
			return &hdhomerun.DeviceInfo{DeviceID: "D1", BaseURL: "http://192.168.1.50", TunerCount: 2}, nil
		},
		lineup: func(context.Context, string) ([]hdhomerun.LineupChannel, error) {
			return nil, errors.New("lineup unreachable")
		},
	})

	first, err := svc.AddTuner(context.Background(), AddTunerInput{URL: "http://192.168.1.50"})
	if err != nil || first == nil || first.Status != "error" || first.LastError == "" {
		t.Fatalf("first add = %+v, %v; want the tuner with its scan failure", first, err)
	}
	again, err := svc.AddTuner(context.Background(), AddTunerInput{URL: "http://192.168.1.50"})
	if err != nil || again == nil || again.ID != first.ID {
		t.Fatalf("second add = %+v, %v; want tuner %s again", again, err, first.ID)
	}
	if tuners, _ := store.ListTuners(context.Background()); len(tuners) != 1 {
		t.Fatalf("tuners = %+v; want one", tuners)
	}
}

// A scan that fails without recording a failure on the tuner is still an
// error: nothing on the saved tuner would show it.
func TestAddTunerFailsWhenTheScanFailsWithoutRecordingIt(t *testing.T) {
	svc := NewServiceWithStore(channelReplaceFailingStore{newMemoryStore()})
	svc.SetHDHomeRunClient(fakeHDHRClient{
		discover: func(context.Context, string, string) (*hdhomerun.DeviceInfo, error) {
			return &hdhomerun.DeviceInfo{DeviceID: "D1", BaseURL: "http://192.168.1.50", TunerCount: 2}, nil
		},
	})
	if tuner, err := svc.AddTuner(context.Background(), AddTunerInput{URL: "http://192.168.1.50"}); !errors.Is(err, errStoreBoom) {
		t.Fatalf("add = %+v, %v; want the store error", tuner, err)
	}
}

type channelReplaceFailingStore struct{ *memoryStore }

func (channelReplaceFailingStore) ReplaceChannelsForTuner(context.Context, string, []Channel) error {
	return errStoreBoom
}
