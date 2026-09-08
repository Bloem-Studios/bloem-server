package handlers

import (
	"errors"
	"net/url"
	"strings"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// Publish only the selected proxy origin and the existing immutable subtitle
// identity pins. Viewer credentials stay in the client's captured request
// authority; no access bearer is copied into the durable decision or recipe.
func bindInitialProxyAuxiliaryURLsV3(plan *playback.PlanV3, profile string) error {
	if plan == nil || plan.SessionID == "" || profile == "" {
		return errors.New("bound auxiliary plan identity required")
	}
	stream, err := url.Parse(plan.Stream.URL)
	if err != nil || (stream.Scheme != "http" && stream.Scheme != "https") || stream.Host == "" || stream.User != nil {
		return errors.New("captured proxy stream origin required")
	}
	index := strings.LastIndex(stream.EscapedPath(), "/stream/")
	if index < 0 {
		return errors.New("captured proxy stream path required")
	}
	prefix := stream.EscapedPath()[:index]
	bind := func(raw string) (string, error) {
		if raw == "" {
			return "", nil
		}
		u, err := url.Parse(raw)
		if err != nil || u.Fragment != "" || u.User != nil {
			return "", errors.New("invalid captured auxiliary URL")
		}
		path := strings.TrimPrefix(u.Path, "/api/v1")
		marker := "/stream/" + plan.SessionID + "/subtitles/"
		if u.IsAbs() {
			if u.Scheme != stream.Scheme || u.Host != stream.Host || !strings.HasPrefix(u.EscapedPath(), prefix+"/stream/v3/"+plan.SessionID+"/subtitles/") {
				return "", errors.New("auxiliary origin or session changed")
			}
			path = strings.TrimPrefix(u.EscapedPath(), prefix)
		} else {
			if !strings.HasPrefix(path, marker) || u.Host != "" {
				return "", errors.New("auxiliary session changed")
			}
			path = "/stream/v3/" + plan.SessionID + "/subtitles/" + strings.TrimPrefix(path, marker)
		}
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return "", errors.New("invalid captured auxiliary query")
		}
		query.Del(streamTokenParam)
		if query.Get("file_id") == "" {
			return "", errors.New("captured auxiliary file identity required")
		}
		out := *stream
		out.RawPath = prefix + path
		out.Path, err = url.PathUnescape(out.RawPath)
		if err != nil {
			return "", err
		}
		out.RawQuery, out.Fragment = query.Encode(), ""
		return out.String(), nil
	}
	for i := range plan.Subtitle.Inventory {
		item := &plan.Subtitle.Inventory[i]
		if item.URL, err = bind(item.URL); err != nil {
			return err
		}
		if item.FontBundleURL, err = bind(item.FontBundleURL); err != nil {
			return err
		}
	}
	if plan.Subtitle.Artifact != nil {
		if plan.Subtitle.Artifact.URL, err = bind(plan.Subtitle.Artifact.URL); err != nil {
			return err
		}
	}
	if plan.Stream.Headers == nil {
		plan.Stream.Headers = make(map[string]string)
	}
	plan.Stream.Headers["X-Profile-Id"] = profile
	return nil
}
