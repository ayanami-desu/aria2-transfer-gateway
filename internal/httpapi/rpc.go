package httpapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"aria2-transfer-gateway/internal/transfer"
)

const (
	rpcEndpointPath = "/jsonrpc"
	rpcMaxBodySize  = 64 << 20

	rpcInvalidRequestCode = -32600
	rpcInternalErrorCode  = -32000
)

type rpcCall struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  string          `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (s *Server) authorizedRequest(r *http.Request) bool {
	if s.authorized(r) {
		return true
	}
	if r.URL.Path != rpcEndpointPath {
		return false
	}
	body, err := readRPCBody(r)
	if err != nil {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return s.rpcBodyAuthorized(body)
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := readRPCBody(r)
	if err != nil {
		writeRPCPayload(w, rpcErrorPayload(rpcCall{}, rpcInvalidRequestCode, err))
		return
	}
	calls, batch, err := decodeRPCCalls(body)
	if err != nil {
		writeRPCPayload(w, rpcErrorPayload(rpcCall{}, rpcInvalidRequestCode, err))
		return
	}

	responses := make([]json.RawMessage, 0, len(calls))
	for _, call := range calls {
		if call.JSONRPC != "2.0" || strings.TrimSpace(call.Method) == "" {
			if rpcCallHasID(call) {
				responses = append(responses, rpcErrorPayload(call, rpcInvalidRequestCode, fmt.Errorf("invalid JSON-RPC request")))
			}
			continue
		}

		if isManagedRPCMethod(call.Method) {
			gid, err := s.createRPCManagedTask(r.Context(), call)
			if !rpcCallHasID(call) {
				continue
			}
			if err != nil {
				responses = append(responses, rpcErrorPayload(call, rpcInternalErrorCode, err))
				continue
			}
			responses = append(responses, rpcResultPayload(call, gid))
			continue
		}

		forwarded, hasResponse, err := s.forwardRPCCall(r.Context(), call)
		if !rpcCallHasID(call) {
			continue
		}
		if err != nil {
			responses = append(responses, rpcErrorPayload(call, rpcInternalErrorCode, err))
			continue
		}
		if hasResponse {
			responses = append(responses, forwarded)
		}
	}
	writeRPCPayloads(w, responses, batch)
}

func (s *Server) createRPCManagedTask(ctx context.Context, call rpcCall) (string, error) {
	input, err := taskInputFromRPCCall(call)
	if err != nil {
		return "", err
	}
	task, err := s.service.Create(ctx, input)
	if err != nil {
		return "", err
	}
	return task.GID, nil
}

func (s *Server) forwardRPCCall(ctx context.Context, call rpcCall) (json.RawMessage, bool, error) {
	if strings.TrimSpace(s.rpcEndpoint) == "" {
		return nil, false, errors.New("aria2 RPC endpoint is not configured")
	}
	params, err := decodeRPCParams(call.Params)
	if err != nil {
		return nil, false, err
	}
	params, err = rewriteRPCParams(params, s.rpcSecret)
	if err != nil {
		return nil, false, err
	}
	call.Params, err = json.Marshal(params)
	if err != nil {
		return nil, false, fmt.Errorf("encode RPC parameters: %w", err)
	}
	requestBody, err := json.Marshal(call)
	if err != nil {
		return nil, false, fmt.Errorf("encode RPC request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.rpcEndpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, false, fmt.Errorf("create aria2 RPC request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := s.rpcHTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, false, fmt.Errorf("forward aria2 RPC request: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, false, fmt.Errorf("read aria2 RPC response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, false, fmt.Errorf("aria2 RPC returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	responseBody = bytes.TrimSpace(responseBody)
	if len(responseBody) == 0 {
		return nil, false, nil
	}
	if !json.Valid(responseBody) {
		return nil, false, errors.New("aria2 RPC returned invalid JSON")
	}
	return json.RawMessage(responseBody), true, nil
}

func readRPCBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, rpcMaxBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("read RPC request: %w", err)
	}
	if len(body) > rpcMaxBodySize {
		return nil, fmt.Errorf("RPC request exceeds %d bytes", rpcMaxBodySize)
	}
	return body, nil
}

func decodeRPCCalls(body []byte) ([]rpcCall, bool, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, false, errors.New("RPC request is empty")
	}
	if body[0] != '[' {
		var call rpcCall
		if err := json.Unmarshal(body, &call); err != nil {
			return nil, false, fmt.Errorf("decode RPC request: %w", err)
		}
		return []rpcCall{call}, false, nil
	}
	var rawCalls []json.RawMessage
	if err := json.Unmarshal(body, &rawCalls); err != nil {
		return nil, true, fmt.Errorf("decode RPC batch: %w", err)
	}
	if len(rawCalls) == 0 {
		return nil, true, errors.New("RPC batch is empty")
	}
	calls := make([]rpcCall, len(rawCalls))
	for index, rawCall := range rawCalls {
		if err := json.Unmarshal(rawCall, &calls[index]); err != nil {
			return nil, true, fmt.Errorf("decode RPC batch item %d: %w", index, err)
		}
	}
	return calls, true, nil
}

func decodeRPCParams(raw json.RawMessage) ([]json.RawMessage, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var params []json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("RPC params must be an array: %w", err)
	}
	return params, nil
}

func rpcCallHasID(call rpcCall) bool {
	return len(call.ID) > 0 && !bytes.Equal(bytes.TrimSpace(call.ID), []byte("null"))
}

func rpcTokenFromParams(params []json.RawMessage) (string, bool) {
	if len(params) == 0 {
		return "", false
	}
	var value string
	if err := json.Unmarshal(params[0], &value); err != nil || !strings.HasPrefix(value, "token:") {
		return "", false
	}
	return strings.TrimPrefix(value, "token:"), true
}

func (s *Server) rpcBodyAuthorized(body []byte) bool {
	if s.token == "" && s.rpcSecret == "" {
		return true
	}
	calls, _, err := decodeRPCCalls(body)
	if err != nil {
		return false
	}
	for _, call := range calls {
		params, err := decodeRPCParams(call.Params)
		if err != nil {
			return false
		}
		provided, ok := rpcTokenFromParams(params)
		if !ok || (!rpcSecretEqual(provided, s.token) && !rpcSecretEqual(provided, s.rpcSecret)) {
			return false
		}
	}
	return true
}

func rpcSecretEqual(provided, expected string) bool {
	if expected == "" || len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func rewriteRPCParams(params []json.RawMessage, secret string) ([]json.RawMessage, error) {
	if _, ok := rpcTokenFromParams(params); ok {
		params = params[1:]
	}
	if secret == "" {
		return params, nil
	}
	token, err := json.Marshal("token:" + secret)
	if err != nil {
		return nil, fmt.Errorf("encode aria2 RPC token: %w", err)
	}
	result := make([]json.RawMessage, 0, len(params)+1)
	result = append(result, token)
	result = append(result, params...)
	return result, nil
}

func taskInputFromRPCCall(call rpcCall) (transfer.TaskInput, error) {
	params, err := decodeRPCParams(call.Params)
	if err != nil {
		return transfer.TaskInput{}, err
	}
	if _, ok := rpcTokenFromParams(params); ok {
		params = params[1:]
	}
	input := transfer.TaskInput{
		Type:          "urls",
		DestinationID: "",
		TargetPath:    "/",
		Cleanup:       true,
	}
	optionsIndex := -1
	switch call.Method {
	case "aria2.addUri":
		if len(params) < 1 || len(params) > 2 {
			return transfer.TaskInput{}, errors.New("aria2.addUri expects urls and optional options")
		}
		if err := json.Unmarshal(params[0], &input.URLs); err != nil {
			return transfer.TaskInput{}, fmt.Errorf("decode aria2.addUri URLs: %w", err)
		}
		if len(params) == 2 {
			optionsIndex = 1
		}
	case "aria2.addTorrent":
		input.Type = "torrent"
		if len(params) < 1 || len(params) > 3 {
			return transfer.TaskInput{}, errors.New("aria2.addTorrent expects content, optional uris, and options")
		}
		if err := json.Unmarshal(params[0], &input.Content); err != nil {
			return transfer.TaskInput{}, fmt.Errorf("decode aria2.addTorrent content: %w", err)
		}
		if len(params) >= 2 {
			var uris []string
			if err := json.Unmarshal(params[1], &uris); err != nil {
				return transfer.TaskInput{}, fmt.Errorf("decode aria2.addTorrent uris: %w", err)
			}
		}
		if len(params) == 3 {
			optionsIndex = 2
		}
	case "aria2.addMetalink":
		input.Type = "metalink"
		if len(params) < 1 || len(params) > 2 {
			return transfer.TaskInput{}, errors.New("aria2.addMetalink expects content and optional options")
		}
		if err := json.Unmarshal(params[0], &input.Content); err != nil {
			return transfer.TaskInput{}, fmt.Errorf("decode aria2.addMetalink content: %w", err)
		}
		if len(params) == 2 {
			optionsIndex = 1
		}
	default:
		return transfer.TaskInput{}, fmt.Errorf("RPC method %q is not managed", call.Method)
	}
	if optionsIndex >= 0 {
		if err := json.Unmarshal(params[optionsIndex], &input.Options); err != nil {
			return transfer.TaskInput{}, fmt.Errorf("decode %s options: %w", call.Method, err)
		}
	}
	input.Pause = rpcPauseOption(input.Options)
	removeRPCOption(input.Options, "pause")
	return input, nil
}

func rpcPauseOption(options map[string]any) bool {
	for key, value := range options {
		if !strings.EqualFold(key, "pause") {
			continue
		}
		switch value := value.(type) {
		case bool:
			return value
		case string:
			return strings.EqualFold(strings.TrimSpace(value), "true")
		}
	}
	return false
}

func removeRPCOption(options map[string]any, name string) {
	for key := range options {
		if strings.EqualFold(key, name) {
			delete(options, key)
		}
	}
}

func isManagedRPCMethod(method string) bool {
	switch method {
	case "aria2.addUri", "aria2.addTorrent", "aria2.addMetalink":
		return true
	default:
		return false
	}
}

func rpcResultPayload(call rpcCall, gid string) json.RawMessage {
	payload, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: call.ID, Result: gid})
	return payload
}

func rpcErrorPayload(call rpcCall, code int, err error) json.RawMessage {
	payload, _ := json.Marshal(rpcResponse{
		JSONRPC: "2.0",
		ID:      call.ID,
		Error:   &rpcError{Code: code, Message: err.Error()},
	})
	return payload
}

func writeRPCPayload(w http.ResponseWriter, payload json.RawMessage) {
	writeRPCPayloads(w, []json.RawMessage{payload}, false)
}

func writeRPCPayloads(w http.ResponseWriter, payloads []json.RawMessage, batch bool) {
	if len(payloads) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if batch {
		_ = json.NewEncoder(w).Encode(payloads)
		return
	}
	_, _ = w.Write(payloads[0])
}
