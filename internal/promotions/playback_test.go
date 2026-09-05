package promotions

import (
	"context"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"testing"
)

type playbackStore struct {
	userstore.UserStore
	profile *userstore.Profile
}

func (s playbackStore) GetProfile(context.Context, string) (*userstore.Profile, error) {
	return s.profile, nil
}

type playbackProvider struct{ store userstore.UserStore }

func (p playbackProvider) ForUser(context.Context, int) (userstore.UserStore, error) {
	return p.store, nil
}
func (p playbackProvider) Close() error { return nil }
func TestPlaybackExcludesKidsAndUnknownProfilesBeforeDatabase(t *testing.T) {
	for _, profile := range []*userstore.Profile{nil, {ID: "child", IsChild: true}} {
		s := NewService(nil, nil, playbackProvider{playbackStore{profile: profile}})
		cards, err := s.Active(context.Background(), Query{Surface: SurfaceInPlayback, ContentID: "episode", Viewer: Viewer{UserID: 1, ProfileID: "child"}})
		if err != nil || len(cards) != 0 {
			t.Fatalf("delivered to child/unknown: %v %v", cards, err)
		}
	}
	s := NewService(nil, nil, nil)
	cards, err := s.Active(context.Background(), Query{Surface: SurfaceInPlayback, ContentID: "episode"})
	if err != nil || len(cards) != 0 {
		t.Fatal("missing profile provider must be closed")
	}
}
func TestPlaybackValidation(t *testing.T) {
	in := validInput()
	in.Surfaces = []string{SurfaceInPlayback}
	if _, err := Normalize(in); err == nil {
		t.Fatal("missing presentation accepted")
	}
	in.Placement.PlaybackStyle = "pip"
	in.Placement.VideoURL = "https://example.com/clip.mp4"
	if _, err := Normalize(in); err != nil {
		t.Fatal(err)
	}
	no := false
	in.Dismissible = &no
	if _, err := Normalize(in); err == nil {
		t.Fatal("non dismissible playback accepted")
	}
	in.Dismissible = nil
	in.Placement.VideoURL = "https://"
	if _, err := Normalize(in); err == nil {
		t.Fatal("invalid video URL accepted")
	}
}

func TestPlaybackDuration(t *testing.T) {
	for _, seconds := range []int{0, 5, 17, 60, -1, 4, 61} {
		in := validInput()
		in.Surfaces = []string{SurfaceInPlayback}
		in.Placement = Placement{PlaybackStyle: "card", DurationSeconds: seconds}
		got, err := Normalize(in)
		valid := seconds == 0 || (seconds >= 5 && seconds <= 60)
		if (err == nil) != valid {
			t.Fatalf("duration %d: %v", seconds, err)
		}
		if seconds == 0 && got.Placement.DurationSeconds != 10 {
			t.Fatal("default must be ten seconds")
		}
	}
	if got := (Promotion{Placement: Placement{PlaybackStyle: "card"}}).Card().DurationSeconds; got != 10 {
		t.Fatalf("legacy duration = %d", got)
	}
}
