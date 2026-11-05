package tracker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxBodySize = 1 << 20

type Source struct {
	url    string
	client *http.Client
	mu     sync.RWMutex
	list   []string
}

func NewSource(url string, client *http.Client) *Source {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Source{url: url, client: client}
}

func (s *Source) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.list...)
}

func (s *Source) Refresh(ctx context.Context) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return 0, fmt.Errorf("create tracker request: %w", err)
	}
	req.Header.Set("User-Agent", "aria2-transfer-gateway")
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch tracker list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("fetch tracker list: unexpected HTTP status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if err != nil {
		return 0, fmt.Errorf("read tracker list: %w", err)
	}
	if len(body) > maxBodySize {
		return 0, fmt.Errorf("tracker list exceeds 1 MiB")
	}
	list := ParseList(string(body))
	if len(list) == 0 {
		return 0, fmt.Errorf("tracker list contains no valid trackers")
	}
	s.mu.Lock()
	s.list = list
	s.mu.Unlock()
	return len(list), nil
}

func ParseList(text string) []string {
	seen := make(map[string]struct{})
	var list []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, rawEntry := range strings.Split(line, ",") {
			entry := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rawEntry), "\ufeff"))
			if entry == "" || strings.HasPrefix(entry, "#") {
				continue
			}
			parsed, err := url.Parse(entry)
			if err != nil || parsed.Host == "" {
				continue
			}
			scheme := strings.ToLower(parsed.Scheme)
			if scheme != "http" && scheme != "https" && scheme != "udp" {
				continue
			}
			if _, exists := seen[entry]; exists {
				continue
			}
			seen[entry] = struct{}{}
			list = append(list, entry)
		}
	}
	return list
}
