package listcollections

import "testing"

func TestRequestReplayIdentityIgnoresLabelsAndSeparatesActorsAndFormats(t *testing.T) {
	s := Subject{UserID: 1, ProfileID: "profile", InstanceID: "instance"}
	e := Entry{Kind: "movie", Title: "Original", ExternalIDs: map[string]string{"tmdb": "42"}}
	key, err := RequestReplayKey(s, e)
	if err != nil {
		t.Fatal(err)
	}
	e.Title = "Renamed"
	e.ExternalIDs["imdb"] = "tt0000042"
	got, err := RequestReplayKey(s, e)
	if err != nil || got != key {
		t.Fatal("mutable metadata changed replay identity", got, err)
	}
	s.ProfileID = "other"
	got, err = RequestReplayKey(s, e)
	if err != nil || got == key {
		t.Fatal("profiles shared replay identity", err)
	}
	s.ProfileID = "profile"
	e.Kind = "series"
	got, err = RequestReplayKey(s, e)
	if err != nil || got == key {
		t.Fatal("formats shared replay identity", err)
	}
	for _, e = range []Entry{{Kind: "movie", ExternalIDs: map[string]string{"imdb": "tt0000042"}}, {Kind: "ebook", ExternalIDs: map[string]string{"asin": "B000000001"}}, {Kind: "audiobook", ExternalIDs: map[string]string{}}} {
		if _, err = RequestReplayKey(s, e); err == nil {
			t.Fatal("unsupported identity admitted", e)
		}
	}
}
