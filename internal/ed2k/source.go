package ed2k

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxBodySize = 1 << 20
	maxServers  = 200
)

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
		return 0, fmt.Errorf("create ed2k server request: %w", err)
	}
	req.Header.Set("User-Agent", "aria2-transfer-gateway")
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch ed2k server list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("fetch ed2k server list: unexpected HTTP status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if err != nil {
		return 0, fmt.Errorf("read ed2k server list: %w", err)
	}
	if len(body) > maxBodySize {
		return 0, fmt.Errorf("ed2k server list exceeds 1 MiB")
	}
	list := ParseList(body)
	if len(list) == 0 {
		return 0, fmt.Errorf("ed2k server list contains no valid servers")
	}
	s.mu.Lock()
	s.list = list
	s.mu.Unlock()
	return len(list), nil
}

// ParseList accepts an eMule server.met file or a text list of HOST:PORT lines.
func ParseList(data []byte) []string {
	if list := parseServerMet(data); len(list) > 0 {
		return list
	}
	return parseTextList(data)
}

func parseServerMet(data []byte) []string {
	if len(data) < 5 {
		return nil
	}
	switch data[0] {
	case 0x0e, 0x0f, 0xe0:
	default:
		return nil
	}
	count := binary.LittleEndian.Uint32(data[1:5])
	offset := 5
	list := make([]string, 0, count)
	seen := make(map[string]struct{}, count)
	for i := uint32(0); i < count; i++ {
		if offset+6 > len(data) {
			break
		}
		host := net.IPv4(data[offset], data[offset+1], data[offset+2], data[offset+3]).String()
		port := binary.LittleEndian.Uint16(data[offset+4 : offset+6])
		offset += 6
		if offset+4 > len(data) {
			break
		}
		tags := binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4
		valid := true
		for j := uint32(0); j < tags; j++ {
			offset, valid = skipTag(data, offset)
			if !valid {
				break
			}
		}
		if !valid {
			break
		}
		if port == 0 {
			continue
		}
		entry := net.JoinHostPort(host, strconv.Itoa(int(port)))
		if _, exists := seen[entry]; exists {
			continue
		}
		seen[entry] = struct{}{}
		list = append(list, entry)
		if len(list) == maxServers {
			break
		}
	}
	return list
}

func skipTag(data []byte, offset int) (int, bool) {
	if offset >= len(data) {
		return offset, false
	}
	tagType := data[offset] & 0x7f
	hasNameID := data[offset]&0x80 != 0
	offset++
	if hasNameID {
		if offset >= len(data) {
			return offset, false
		}
		offset++
	} else {
		if offset+2 > len(data) {
			return offset, false
		}
		nameLength := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
		offset += 2
		if offset+nameLength > len(data) {
			return offset, false
		}
		offset += nameLength
	}

	switch tagType {
	case 0x01:
		offset += 16
	case 0x02:
		if offset+2 > len(data) {
			return offset, false
		}
		valueLength := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
		offset += 2 + valueLength
	case 0x03:
		offset += 4
	case 0x07:
		if offset+4 > len(data) {
			return offset, false
		}
		valueLength := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4 + valueLength
	case 0x08:
		offset += 2
	case 0x09:
		offset++
	default:
		return offset, false
	}

	if offset > len(data) {
		return offset, false
	}
	return offset, true
}

func parseTextList(data []byte) []string {
	text := strings.TrimPrefix(string(data), "\ufeff")
	lines := strings.Split(text, "\n")
	list := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, rawEntry := range strings.Split(line, ",") {
			entry := normalizeServer(strings.TrimSpace(rawEntry))
			if entry == "" {
				continue
			}
			if _, exists := seen[entry]; exists {
				continue
			}
			seen[entry] = struct{}{}
			list = append(list, entry)
			if len(list) == maxServers {
				return list
			}
		}
	}
	return list
}

func normalizeServer(value string) string {
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "ed2k://|server|") {
		parts := strings.Split(value, "|")
		if len(parts) < 4 {
			return ""
		}
		value = net.JoinHostPort(strings.TrimSpace(parts[2]), strings.TrimSpace(parts[3]))
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil || strings.TrimSpace(host) == "" {
		return ""
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(number))
}
