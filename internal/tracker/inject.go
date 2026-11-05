package tracker

import (
	"context"
	"strings"

	"aria2-transfer-gateway/internal/aria2"
)

const maxTrackers = 200

type Downloader struct {
	aria2.Downloader
	source *Source
}

func NewDownloader(base aria2.Downloader, source *Source) *Downloader {
	return &Downloader{Downloader: base, source: source}
}

func (d *Downloader) AddTorrent(ctx context.Context, content, dir string, pause bool, options map[string]any) (string, error) {
	return d.Downloader.AddTorrent(ctx, content, dir, pause, mergeOptions(options, d.cachedTrackers()))
}

func (d *Downloader) AddURI(ctx context.Context, urls []string, dir string, pause bool, options map[string]any) (string, error) {
	for _, rawURL := range urls {
		if strings.HasPrefix(strings.ToLower(rawURL), "magnet:") {
			return d.Downloader.AddURI(ctx, urls, dir, pause, mergeOptions(options, d.cachedTrackers()))
		}
	}
	return d.Downloader.AddURI(ctx, urls, dir, pause, options)
}

func (d *Downloader) cachedTrackers() []string {
	if d == nil || d.source == nil {
		return nil
	}
	return d.source.List()
}

func mergeOptions(options map[string]any, cached []string) map[string]any {
	if len(cached) == 0 {
		return options
	}
	merged := make(map[string]any, len(options)+1)
	for key, value := range options {
		merged[key] = value
	}

	trackers := make([]string, 0, len(cached))
	seen := make(map[string]struct{}, len(cached))
	if value, ok := options["bt-tracker"].(string); ok {
		for _, tracker := range strings.Split(value, ",") {
			tracker = strings.TrimSpace(tracker)
			if tracker == "" {
				continue
			}
			if _, exists := seen[tracker]; exists {
				continue
			}
			seen[tracker] = struct{}{}
			trackers = append(trackers, tracker)
			if len(trackers) == maxTrackers {
				break
			}
		}
	}
	for _, tracker := range cached {
		if len(trackers) == maxTrackers {
			break
		}
		if _, exists := seen[tracker]; exists {
			continue
		}
		seen[tracker] = struct{}{}
		trackers = append(trackers, tracker)
	}
	merged["bt-tracker"] = strings.Join(trackers, ",")
	return merged
}
