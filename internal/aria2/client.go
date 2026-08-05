package aria2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
)

type DownloadFile struct {
	Path            string `json:"path"`
	Length          string `json:"length"`
	CompletedLength string `json:"completedLength"`
	Selected        bool   `json:"selected"`
}
type DownloadStatus struct {
	Status          string `json:"status"`
	CompletedLength string `json:"completedLength"`
	TotalLength     string `json:"totalLength"`
}

func (f *DownloadFile) UnmarshalJSON(data []byte) error {
	var raw struct {
		Path            string          `json:"path"`
		Length          string          `json:"length"`
		CompletedLength string          `json:"completedLength"`
		Selected        json.RawMessage `json:"selected"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	selected, err := decodeJSONBool(raw.Selected)
	if err != nil {
		return err
	}
	*f = DownloadFile{
		Path:            raw.Path,
		Length:          raw.Length,
		CompletedLength: raw.CompletedLength,
		Selected:        selected,
	}
	return nil
}

func decodeJSONBool(data json.RawMessage) (bool, error) {
	var value bool
	if err := json.Unmarshal(data, &value); err == nil {
		return value, nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return false, fmt.Errorf("decode aria2 boolean: %w", err)
	}
	value, err := strconv.ParseBool(text)
	if err != nil {
		return false, fmt.Errorf("decode aria2 boolean: %w", err)
	}
	return value, nil
}

var ErrGIDNotFound = errors.New("aria2 GID not found")

type Downloader interface {
	AddURI(ctx context.Context, urls []string, dir string, pause bool, options map[string]string) (string, error)
	AddTorrent(ctx context.Context, content, dir string, pause bool, options map[string]string) (string, error)
	AddMetalink(ctx context.Context, content, dir string, pause bool, options map[string]string) (string, error)
	GetFiles(ctx context.Context, gid string) ([]DownloadFile, error)
	GetStatus(ctx context.Context, gid string) (DownloadStatus, error)
	GetFollowedBy(ctx context.Context, gid string) ([]string, error)
	Remove(ctx context.Context, gid string) error
}

type Client struct {
	endpoint string
	secret   string
	http     *http.Client
	id       atomic.Uint64
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error,omitempty"`
}

type versionResult struct {
	Version        string   `json:"version"`
	EnabledFeature []string `json:"enabledFeatures"`
}

func NewClient(endpoint, secret string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{endpoint: endpoint, secret: secret, http: httpClient}
}

func (c *Client) AddURI(ctx context.Context, urls []string, dir string, pause bool, options map[string]string) (string, error) {
	return c.add(ctx, "aria2.addUri", []any{urls}, dir, pause, options)
}

func (c *Client) AddTorrent(ctx context.Context, content, dir string, pause bool, options map[string]string) (string, error) {
	return c.add(ctx, "aria2.addTorrent", []any{content}, dir, pause, options)
}

func (c *Client) AddMetalink(ctx context.Context, content, dir string, pause bool, options map[string]string) (string, error) {
	return c.add(ctx, "aria2.addMetalink", []any{content}, dir, pause, options)
}

func (c *Client) GetFiles(ctx context.Context, gid string) ([]DownloadFile, error) {
	var files []DownloadFile
	if err := c.call(ctx, "aria2.getFiles", []any{gid}, &files); err != nil {
		return nil, err
	}
	return files, nil
}
func (c *Client) GetStatus(ctx context.Context, gid string) (DownloadStatus, error) {
	var status DownloadStatus
	if err := c.call(ctx, "aria2.tellStatus", []any{gid, []string{"status", "completedLength", "totalLength"}}, &status); err != nil {
		return DownloadStatus{}, err
	}
	return status, nil
}

func (c *Client) GetFollowedBy(ctx context.Context, gid string) ([]string, error) {
	var status struct {
		FollowedBy []string `json:"followedBy"`
	}
	if err := c.call(ctx, "aria2.tellStatus", []any{gid, []string{"followedBy"}}, &status); err != nil {
		return nil, err
	}
	return status.FollowedBy, nil
}

func (c *Client) Remove(ctx context.Context, gid string) error {
	return c.call(ctx, "aria2.forceRemove", []any{gid}, nil)
}

func (c *Client) add(ctx context.Context, method string, params []any, dir string, pause bool, options map[string]string) (string, error) {
	var gid string
	if err := c.call(ctx, method, append(params, buildOptions(dir, pause, options)), &gid); err != nil {
		return "", err
	}
	return gid, nil
}

func buildOptions(dir string, pause bool, options map[string]string) map[string]string {
	result := make(map[string]string, len(options)+2)
	for key, value := range options {
		result[key] = value
	}
	result["dir"] = dir
	if pause {
		result["pause"] = "true"
	}
	return result
}

func (c *Client) GetVersion(ctx context.Context) (versionResult, error) {
	var result versionResult
	if err := c.call(ctx, "aria2.getVersion", nil, &result); err != nil {
		return versionResult{}, err
	}
	return result, nil
}

func (c *Client) SaveSession(ctx context.Context) error {
	return c.call(ctx, "aria2.saveSession", nil, nil)
}

func (c *Client) ChangeGlobalOption(ctx context.Context, options map[string]string) error {
	return c.call(ctx, "aria2.changeGlobalOption", []any{options}, nil)
}

func (c *Client) call(ctx context.Context, method string, methodParams []any, result any) error {
	id := c.id.Add(1)
	params := make([]any, 0, len(methodParams)+1)
	if c.secret != "" {
		params = append(params, "token:"+c.secret)
	}
	params = append(params, methodParams...)
	requestBody := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      uint64 `json:"id"`
		Method  string `json:"method"`
		Params  []any  `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("encode aria2 request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create aria2 request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call aria2 %s: %w", method, err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read aria2 response: %w", err)
	}
	var response rpcResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("aria2 %s returned HTTP %d: %s", method, resp.StatusCode, string(responseBody))
		}
		return fmt.Errorf("decode aria2 response: %w", err)
	}
	if response.Error != nil {
		message := fmt.Errorf("aria2 %s failed (%d): %s", method, response.Error.Code, response.Error.Message)
		lowerMessage := strings.ToLower(response.Error.Message)
		if strings.Contains(lowerMessage, "gid") && (strings.Contains(lowerMessage, "not found") || strings.Contains(lowerMessage, "not exist")) {
			return fmt.Errorf("%w: %v", ErrGIDNotFound, message)
		}
		return message
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("aria2 %s returned HTTP %d: %s", method, resp.StatusCode, string(responseBody))
	}
	if result == nil || len(response.Result) == 0 || string(response.Result) == "null" {
		return nil
	}
	if err := json.Unmarshal(response.Result, result); err != nil {
		return fmt.Errorf("decode aria2 %s result: %w", method, err)
	}
	return nil
}
