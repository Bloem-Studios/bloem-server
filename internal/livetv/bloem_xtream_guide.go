package livetv

import (
	"bufio"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
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
	return time.Time{}, errors.New("Xtream XMLTV programme has an invalid timestamp")
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

func parseXtreamGuide(reader io.Reader, sourceID string, channels []Channel, from, to time.Time) ([]Program, error) {
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
			return nil, errors.New("Xtream XMLTV feed is malformed or exceeds its size limit")
		}
		switch value := token.(type) {
		case xml.CharData:
			if strings.TrimSpace(string(value)) != "" {
				return nil, errors.New("Xtream XMLTV feed contains unexpected text")
			}
		case xml.StartElement:
			if !root {
				if value.Name.Local != "tv" {
					return nil, errors.New("Xtream provider did not return XMLTV")
				}
				root = true
				continue
			}
			if closed {
				return nil, errors.New("Xtream XMLTV feed has multiple roots")
			}
			if value.Name.Local != "programme" {
				if err := decoder.Skip(); err != nil {
					return nil, errors.New("Xtream XMLTV feed is malformed")
				}
				continue
			}
			entries++
			if entries > 1000000 {
				return nil, errors.New("Xtream XMLTV entry limit exceeded")
			}
			var entry xtreamXMLProgramme
			if err := decoder.DecodeElement(&entry, &value); err != nil {
				return nil, errors.New("Xtream XMLTV programme is malformed")
			}
			matches := stations[entry.Channel]
			if len(matches) == 0 {
				continue
			}
			start, err := xtreamXMLTime(entry.Start)
			if err != nil {
				return nil, err
			}
			stop, err := xtreamXMLTime(entry.Stop)
			if err != nil {
				return nil, err
			}
			if !stop.After(start) || stop.Sub(start) > 48*time.Hour {
				return nil, errors.New("Xtream XMLTV programme has an invalid duration")
			}
			if !start.Before(to) || !stop.After(from) {
				continue
			}
			title, subtitle, description := xtreamText(entry.Title), xtreamText(entry.Subtitle), xtreamText(entry.Description)
			if title == "" || len(title) > 1024 || len(subtitle) > 1024 || len(description) > 16384 || len(entry.Categories) > 32 {
				return nil, errors.New("Xtream XMLTV programme metadata exceeds limits")
			}
			for _, category := range entry.Categories {
				if len(category) > 256 {
					return nil, errors.New("Xtream XMLTV category exceeds limit")
				}
			}
			for _, channel := range matches {
				id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("bloem-xtream-program:"+sourceID+":"+channel.ID+":"+start.Format(time.RFC3339))).String()
				if prior, exists := seen[id]; exists {
					if prior.title != title || !prior.stop.Equal(stop) {
						return nil, errors.New("Xtream XMLTV feed has conflicting programme identities")
					}
					continue
				}
				if len(programs) >= xtreamProgramLimit {
					return nil, errors.New("Xtream XMLTV programme limit exceeded")
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
				return nil, errors.New("Xtream XMLTV feed is malformed")
			}
			closed = true
		}
	}
	if !root || !closed || limited.N <= 0 {
		return nil, errors.New("Xtream XMLTV feed is incomplete or exceeds its size limit")
	}
	return programs, nil
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
			return nil, errors.New("Xtream XMLTV entity declarations are not accepted")
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
	programs, err := parseXtreamGuide(body, source.ID, channels, from, to)
	if err != nil {
		return err
	}
	if len(programs) == 0 {
		return errors.New("Xtream guide has no matching programmes in the current two-day window")
	}
	store, err := s.xtreamStore()
	if err != nil {
		return err
	}
	return store.replaceXtreamPrograms(ctx, source, version, channels, programs, from, to)
}
