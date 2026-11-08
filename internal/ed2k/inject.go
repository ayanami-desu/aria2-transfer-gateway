package ed2k

import (
	"context"
	"strings"

	"aria2-transfer-gateway/internal/aria2"
)

type Downloader struct {
	aria2.Downloader
	source *Source
}

func NewDownloader(base aria2.Downloader, source *Source) *Downloader {
	return &Downloader{Downloader: base, source: source}
}

func (d *Downloader) AddURI(ctx context.Context, urls []string, dir string, pause bool, options map[string]any) (string, error) {
	if !hasED2KURL(urls) {
		return d.Downloader.AddURI(ctx, urls, dir, pause, options)
	}
	return d.Downloader.AddURI(ctx, urls, dir, pause, mergeServers(options, d.cachedServers()))
}

func (d *Downloader) cachedServers() []string {
	if d == nil || d.source == nil {
		return nil
	}
	return d.source.List()
}

func hasED2KURL(urls []string) bool {
	for _, rawURL := range urls {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawURL)), "ed2k://") {
			return true
		}
	}
	return false
}

func mergeServers(options map[string]any, cached []string) map[string]any {
	if len(cached) == 0 {
		return options
	}
	merged := make(map[string]any, len(options)+1)
	for key, value := range options {
		merged[key] = value
	}

	servers := make([]string, 0, len(cached)+1)
	seen := make(map[string]struct{}, len(cached)+1)
	if value, ok := options["ed2k-server"].(string); ok {
		for _, server := range strings.Split(value, ",") {
			server = strings.TrimSpace(server)
			if server == "" {
				continue
			}
			if _, exists := seen[server]; exists {
				continue
			}
			seen[server] = struct{}{}
			servers = append(servers, server)
			if len(servers) == maxServers {
				break
			}
		}
	}
	for _, server := range cached {
		if len(servers) == maxServers {
			break
		}
		if _, exists := seen[server]; exists {
			continue
		}
		seen[server] = struct{}{}
		servers = append(servers, server)
	}
	merged["ed2k-server"] = strings.Join(servers, ",")
	return merged
}
