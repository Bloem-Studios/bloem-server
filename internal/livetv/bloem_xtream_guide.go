package livetv

import (
	"bufio"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	GuideSourceXtream  = "xtream"
	xtreamXMLLimit     = 64 << 20
	xtreamProgramLimit = 250000
)

type xtreamXMLProgramme struct {
	Channel     string          `xml:"channel,attr"`
	Start       string          `xml:"start,attr"`
	Stop        string          `xml:"stop,attr"`
	Title       []xtreamXMLText `xml:"title"`
	Subtitle    []xtreamXMLText `xml:"sub-title"`
	Description []xtreamXMLText `xml:"desc"`
	Categories  []string        `xml:"category"`
	Episodes    []struct {
		System string `xml:"system,attr"`
		Value  string `xml:",chardata"`
	} `xml:"episode-num"`
	New      *struct{} `xml:"new"`
	Premiere *struct{} `xml:"premiere"`
	Previous *struct{} `xml:"previously-shown"`
	Live     *struct{} `xml:"live"`
}

type xtreamXMLText struct {
	Lang  string `xml:"lang,attr"`
	Value string `xml:",chardata"`
}

func xtreamText(values []xtreamXMLText) string {
	for _, value := range values {
		if strings.EqualFold(value.Lang, "en") && strings.TrimSpace(value.Value) != "" {
			return strings.TrimSpace(value.Value)
		}
	}
	for _, value := range values {
		if strings.TrimSpace(value.Value) != "" {
			return strings.TrimSpace(value.Value)
		}
	}
	return ""
}

func xtreamXMLTime(value string) (time.Time, error) {
	value = strings.Join(strings.Fields(value), " ")
	for _, layout := range []string{"20060102150405 -0700", "20060102150405-0700", "20060102150405"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("Xtream XMLTV programme has an invalid timestamp") //nolint:staticcheck // ST1005: "Xtream" is a product name
}

func xtreamEpisodeIndex(value string) *int {
	value, _, _ = strings.Cut(strings.TrimSpace(value), "/")
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 || n >= 100000 {
		return nil
	}
	n++
	return &n
}

// xtreamGuideSkips counts in-window programmes for mapped channels that were
// dropped because the entry itself is invalid. Messy IPTV feeds routinely
// contain such entries; they must not prevent the valid ones from publishing.
type xtreamGuideSkips struct {
	InvalidTimestamp    int
	InvalidDuration     int
	MissingTitle        int
	OversizedMetadata   int
	ConflictingIdentity int
}

func (k xtreamGuideSkips) Total() int {
	return k.InvalidTimestamp + k.InvalidDuration + k.MissingTitle + k.OversizedMetadata + k.ConflictingIdentity
}

// String summarizes counts only; it never contains provider data.
func (k xtreamGuideSkips) String() string {
	parts := []string{}
	for _, reason := range []struct {
		n    int
		name string
	}{
		{k.InvalidTimestamp, "invalid timestamp"},
		{k.InvalidDuration, "invalid duration"},
		{k.MissingTitle, "missing title"},
		{k.OversizedMetadata, "oversized metadata"},
		{k.ConflictingIdentity, "conflicting duplicate"},
	} {
		if reason.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", reason.n, reason.name))
		}
	}
	return strings.Join(parts, ", ")
}

// parseXtreamGuide returns the valid programmes overlapping [from, to) for
// the enabled channels. Only structural problems (not XMLTV, malformed or
// oversized document, entity declarations, hard limits) fail the parse;
// invalid individual programmes are skipped and counted.
func parseXtreamGuide(reader io.Reader, sourceID string, channels []Channel, from, to time.Time) ([]Program, xtreamGuideSkips, error) {
	var skipped xtreamGuideSkips
	stations := map[string][]Channel{}
	for _, channel := range channels {
		if !channel.Enabled {
			continue
		}
		station := channel.GuideStationID
		if station == "" {
			station = channel.Number
		}
		if station != "" {
			stations[station] = append(stations[station], channel)
		}
	}
	limited := &io.LimitedReader{R: reader, N: xtreamXMLLimit + 1}
	buffered := bufio.NewReader(limited)
	if prefix, _ := buffered.Peek(3); string(prefix) == "\xef\xbb\xbf" {
		_, _ = buffered.Discard(3)
	}
	decoder := xml.NewTokenDecoder(&xtreamXMLTokens{decoder: xml.NewDecoder(buffered)})
	root, closed := false, false
	programs := []Program{}
	type programmeIdentity struct {
		title string
		stop  time.Time
	}
	seen := map[string]programmeIdentity{}
	entries := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, skipped, errors.New("Xtream XMLTV feed is malformed or exceeds its size limit") //nolint:staticcheck // ST1005: "Xtream" is a product name
		}
		switch value := token.(type) {
		case xml.CharData:
			if strings.TrimSpace(string(value)) != "" {
				return nil, skipped, errors.New("Xtream XMLTV feed contains unexpected text") //nolint:staticcheck // ST1005: "Xtream" is a product name
			}
		case xml.StartElement:
			if !root {
				if value.Name.Local != "tv" {
					return nil, skipped, errors.New("Xtream provider did not return XMLTV") //nolint:staticcheck // ST1005: "Xtream" is a product name
				}
				root = true
				continue
			}
			if closed {
				return nil, skipped, errors.New("Xtream XMLTV feed has multiple roots") //nolint:staticcheck // ST1005: "Xtream" is a product name
			}
			if value.Name.Local != "programme" {
				if err := decoder.Skip(); err != nil {
					return nil, skipped, errors.New("Xtream XMLTV feed is malformed") //nolint:staticcheck // ST1005: "Xtream" is a product name
				}
				continue
			}
			entries++
			if entries > 1000000 {
				return nil, skipped, errors.New("Xtream XMLTV entry limit exceeded") //nolint:staticcheck // ST1005: "Xtream" is a product name
			}
			var entry xtreamXMLProgramme
			if err := decoder.DecodeElement(&entry, &value); err != nil {
				return nil, skipped, errors.New("Xtream XMLTV programme is malformed") //nolint:staticcheck // ST1005: "Xtream" is a product name
			}
			matches := stations[entry.Channel]
			if len(matches) == 0 {
				continue
			}
			start, startErr := xtreamXMLTime(entry.Start)
			stop, stopErr := xtreamXMLTime(entry.Stop)
			if startErr != nil || stopErr != nil {
				// Outside-window entries are dropped silently; an entry whose
				// placement cannot be determined is counted.
				if (startErr == nil && !start.Before(to)) || (stopErr == nil && !stop.After(from)) {
					continue
				}
				skipped.InvalidTimestamp++
				continue
			}
			// Window first: junk outside the published window is irrelevant.
			if !start.Before(to) || !stop.After(from) {
				continue
			}
			if !stop.After(start) || stop.Sub(start) > 48*time.Hour {
				skipped.InvalidDuration++
				continue
			}
			title, subtitle, description := xtreamText(entry.Title), xtreamText(entry.Subtitle), xtreamText(entry.Description)
			if title == "" {
				skipped.MissingTitle++
				continue
			}
			oversized := len(title) > 1024 || len(subtitle) > 1024 || len(description) > 16384 || len(entry.Categories) > 32
			for _, category := range entry.Categories {
				oversized = oversized || len(category) > 256
			}
			if oversized {
				skipped.OversizedMetadata++
				continue
			}
			conflict := false
			for _, channel := range matches {
				id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("bloem-xtream-program:"+sourceID+":"+channel.ID+":"+start.Format(time.RFC3339))).String()
				if prior, exists := seen[id]; exists && (prior.title != title || !prior.stop.Equal(stop)) {
					conflict = true
				}
			}
			if conflict {
				// The first entry for a start time wins; a differing duplicate
				// is dropped for every channel it maps to.
				skipped.ConflictingIdentity++
				continue
			}
			for _, channel := range matches {
				id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("bloem-xtream-program:"+sourceID+":"+channel.ID+":"+start.Format(time.RFC3339))).String()
				if _, exists := seen[id]; exists {
					continue
				}
				if len(programs) >= xtreamProgramLimit {
					return nil, skipped, errors.New("Xtream XMLTV programme limit exceeded") //nolint:staticcheck // ST1005: "Xtream" is a product name
				}
				seen[id] = programmeIdentity{title: title, stop: stop}
				program := Program{ID: id, ChannelID: channel.ID, SourceID: sourceID, ExternalID: id, Start: start, Stop: stop, Title: title, Subtitle: subtitle, Description: description, Genres: entry.Categories, IsNew: (entry.New != nil || entry.Premiere != nil) && entry.Previous == nil, IsLive: entry.Live != nil}
				for _, episode := range entry.Episodes {
					if episode.System != "xmltv_ns" {
						continue
					}
					parts := strings.Split(episode.Value, ".")
					if len(parts) > 0 {
						program.Season = xtreamEpisodeIndex(parts[0])
					}
					if len(parts) > 1 {
						program.Episode = xtreamEpisodeIndex(parts[1])
					}
					break
				}
				programs = append(programs, program)
			}
		case xml.EndElement:
			if value.Name.Local != "tv" || !root || closed {
				return nil, skipped, errors.New("Xtream XMLTV feed is malformed") //nolint:staticcheck // ST1005: "Xtream" is a product name
			}
			closed = true
		}
	}
	if !root || !closed || limited.N <= 0 {
		return nil, skipped, errors.New("Xtream XMLTV feed is incomplete or exceeds its size limit") //nolint:staticcheck // ST1005: "Xtream" is a product name
	}
	return programs, skipped, nil
}

// Guard tokens even inside DecodeElement/Skip; nested declarations must not
// slip through an ignored channel element. encoding/xml never fetches external
// DTDs, and no Entity map or network-capable CharsetReader is installed.
type xtreamXMLTokens struct {
	decoder       *xml.Decoder
	root, doctype bool
}

func (t *xtreamXMLTokens) Token() (xml.Token, error) {
	token, err := t.decoder.Token()
	if err != nil {
		return nil, err
	}
	switch value := token.(type) {
	case xml.StartElement:
		t.root = true
	case xml.Directive:
		fields := strings.Fields(string(value))
		if t.root || t.doctype || len(fields) < 2 || fields[0] != "DOCTYPE" || fields[1] != "tv" || strings.Contains(string(value), "[") {
			return nil, errors.New("Xtream XMLTV entity declarations are not accepted") //nolint:staticcheck // ST1005: "Xtream" is a product name
		}
		t.doctype = true
	}
	return token, nil
}

func (s *Service) prepareXtreamGuideConfig(ctx context.Context, source *GuideSource, cfg map[string]string) error {
	if _, err := s.xtreamStore(); err != nil {
		return err
	}
	id := strings.TrimSpace(cfg["tuner_id"])
	tuner, err := s.store.GetTuner(ctx, id)
	if err != nil {
		return err
	}
	if tuner == nil || tuner.Type != TunerTypeXtream {
		return fmt.Errorf("%w: choose a configured Xtream provider", ErrInvalidArgument)
	}
	// Never copy provider credentials or arbitrary upstream URLs into public
	// guide-source configuration, even when converting an older source kind.
	source.Config = map[string]string{"tuner_id": id}
	if strings.TrimSpace(source.DisplayName) == "" {
		source.DisplayName = "Xtream guide"
	}
	return nil
}

func (s *Service) syncXtreamGuide(ctx context.Context, source *GuideSource, version string) error {
	if !source.Enabled {
		return fmt.Errorf("%w: guide source is disabled", ErrInvalidArgument)
	}
	tunerID := source.Config["tuner_id"]
	tuner, err := s.store.GetTuner(ctx, tunerID)
	if err != nil {
		return err
	}
	if tuner == nil || tuner.Type != TunerTypeXtream {
		return ErrNotFound
	}
	client, err := s.xtreamClientForTuner(ctx, tuner)
	if err != nil {
		return err
	}
	channels, err := s.store.ListChannels(ctx, tunerID)
	if err != nil {
		return err
	}
	body, err := client.openEPG(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	now := s.now()
	from, to := now.Add(-6*time.Hour), now.Add(48*time.Hour)
	programs, skipped, err := parseXtreamGuide(body, source.ID, channels, from, to)
	if err != nil {
		// A body read cut by the sync deadline surfaces as a parse error.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if skipped.Total() > 0 {
		// One summarized warning per sync; counts only, never provider data.
		slog.WarnContext(ctx, "livetv xtream guide skipped invalid programmes",
			"guide_source_id", source.ID, "skipped", skipped.Total(), "published", len(programs),
			"invalid_timestamp", skipped.InvalidTimestamp, "invalid_duration", skipped.InvalidDuration,
			"missing_title", skipped.MissingTitle, "oversized_metadata", skipped.OversizedMetadata,
			"conflicting_duplicate", skipped.ConflictingIdentity)
	}
	if len(programs) == 0 {
		if skipped.Total() > 0 {
			return fmt.Errorf("Xtream guide has no valid programmes in the current two-day window (skipped %s)", skipped) //nolint:staticcheck // ST1005: "Xtream" is a product name
		}
		return errors.New("Xtream guide has no matching programmes in the current two-day window") //nolint:staticcheck // ST1005: "Xtream" is a product name
	}
	store, err := s.xtreamStore()
	if err != nil {
		return err
	}
	return store.replaceXtreamPrograms(ctx, source, version, channels, programs, from, to)
}
