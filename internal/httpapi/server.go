package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"aria2-transfer-gateway/internal/domain"
	"aria2-transfer-gateway/internal/store"
	"aria2-transfer-gateway/internal/transfer"
)

type Server struct {
	service     *transfer.Service
	token       string
	corsOrigins map[string]struct{}
	allowAny    bool
	mux         *http.ServeMux
}

type CreateTaskRequest struct {
	Type          string         `json:"type"`
	URLs          []string       `json:"urls"`
	Content       string         `json:"content"`
	Options       map[string]any `json:"options"`
	DestinationID string         `json:"destination_id"`
	TargetPath    string         `json:"target_path"`
	Cleanup       *bool          `json:"cleanup"`
	Pause         bool           `json:"pause"`
}

type HookRequest struct {
	GID      string `json:"gid"`
	FilePath string `json:"file_path"`
	Reason   string `json:"reason"`
}
type RetryTasksRequest struct {
	IDs  []string `json:"ids"`
	Mode string   `json:"mode"`
}

type RetryTaskFailure struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

type RetryTasksResponse struct {
	Succeeded []domain.TaskView  `json:"succeeded"`
	Failed    []RetryTaskFailure `json:"failed"`
}

type DeleteTasksRequest struct {
	IDs []string `json:"ids"`
}

type DeleteTasksResponse struct {
	Deleted []string           `json:"deleted"`
	Failed  []RetryTaskFailure `json:"failed"`
}
type DeleteTasksByGIDRequest struct {
	GIDs []string `json:"gids"`
}

type DeleteGIDFailure struct {
	GID   string `json:"gid"`
	Error string `json:"error"`
}

type DeleteTasksByGIDResponse struct {
	Deleted  []string           `json:"deleted"`
	NotFound []string           `json:"not_found"`
	Failed   []DeleteGIDFailure `json:"failed"`
}

type destinationRequest struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	Endpoint     string `json:"endpoint"`
	Mount        string `json:"mount"`
	Remote       string `json:"remote"`
	Root         string `json:"root"`
	RcloneConfig string `json:"rclone_config"`
	Token        string `json:"token"`
}

type destinationView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	Endpoint     string `json:"endpoint,omitempty"`
	Mount        string `json:"mount,omitempty"`
	Remote       string `json:"remote,omitempty"`
	Root         string `json:"root,omitempty"`
	RcloneConfig string `json:"rclone_config,omitempty"`
	HasToken     bool   `json:"has_token"`
	IsDefault    bool   `json:"is_default"`
}

func NewServer(service *transfer.Service, token string, corsOrigins []string) *Server {
	server := &Server{
		service: service,
		token:   token,
		mux:     http.NewServeMux(),
	}
	for _, origin := range corsOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "*" {
			server.allowAny = true
			continue
		}
		if origin != "" {
			server.corsOrigins = appendOrigin(server.corsOrigins, origin)
		}
	}
	server.routes()
	return server
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.setCORS(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path != "/healthz" && !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/api/v1/destinations", s.handleDestinations)
	s.mux.HandleFunc("/api/v1/destinations/", s.handleDestinationPath)
	s.mux.HandleFunc("/api/v1/tasks", s.handleTasks)
	s.mux.HandleFunc("/api/v1/tasks/retry", s.handleRetryTasks)
	s.mux.HandleFunc("/api/v1/tasks/delete", s.handleDeleteTasks)
	s.mux.HandleFunc("/api/v1/tasks/delete-by-gid", s.handleDeleteTasksByGID)
	s.mux.HandleFunc("/api/v1/tasks/", s.handleTaskPath)
	s.mux.HandleFunc("/api/v1/hooks/aria2/completed", s.handleCompleted)
	s.mux.HandleFunc("/api/v1/hooks/aria2/stopped", s.handleStopped)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDestinations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		result := make([]destinationView, 0)
		for _, destination := range s.service.Destinations() {
			result = append(result, s.viewDestination(destination))
		}
		writeJSON(w, http.StatusOK, result)
	case http.MethodPost:
		var request destinationRequest
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		destination, err := s.service.CreateDestination(request.destination())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, s.viewDestination(destination))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleDestinationPath(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/destinations/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "destination not found")
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "default" && r.Method == http.MethodPut {
		if err := s.service.SetDefaultDestination(id); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPut {
		var request destinationRequest
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		destination, err := s.service.UpdateDestination(id, request.destination())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s.viewDestination(destination))
		return
	}
	if len(parts) == 1 && r.Method == http.MethodDelete {
		if err := s.service.DeleteDestination(id); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, http.StatusNotFound, "destination route not found")
}

func (request destinationRequest) destination() domain.Destination {
	return domain.Destination{
		ID: request.ID, Name: request.Name, Provider: request.Provider,
		Endpoint: request.Endpoint, Mount: request.Mount, Remote: request.Remote,
		Root: request.Root, RcloneConfig: request.RcloneConfig, Token: request.Token,
	}
}

func (s *Server) viewDestination(destination domain.Destination) destinationView {
	return destinationView{
		ID: destination.ID, Name: destination.Name, Provider: destination.Provider,
		Endpoint: destination.Endpoint, Mount: destination.Mount, Remote: destination.Remote,
		Root: destination.Root, RcloneConfig: destination.RcloneConfig,
		HasToken: destination.Token != "", IsDefault: destination.ID == s.service.DefaultDestinationID(),
	}
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		filter, err := parseTaskFilter(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		tasks, err := s.service.ListFiltered(filter)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		views := make([]domain.TaskView, 0, len(tasks))
		for _, task := range tasks {
			views = append(views, s.service.View(task))
		}
		writeJSON(w, http.StatusOK, views)
	case http.MethodPost:
		var request CreateTaskRequest
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		cleanup := true
		if request.Cleanup != nil {
			cleanup = *request.Cleanup
		}
		task, err := s.service.Create(r.Context(), transfer.TaskInput{
			Type:          request.Type,
			URLs:          request.URLs,
			Content:       request.Content,
			Options:       request.Options,
			DestinationID: request.DestinationID,
			TargetPath:    request.TargetPath,
			Cleanup:       cleanup,
			Pause:         request.Pause,
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, s.service.View(task))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleRetryTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request RetryTasksRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(request.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "at least one task id is required")
		return
	}
	response := RetryTasksResponse{
		Succeeded: make([]domain.TaskView, 0, len(request.IDs)),
		Failed:    make([]RetryTaskFailure, 0),
	}
	seen := make(map[string]struct{}, len(request.IDs))
	for _, rawID := range request.IDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			response.Failed = append(response.Failed, RetryTaskFailure{Error: "task id is required"})
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		task, err := s.service.Retry(r.Context(), id, transfer.RetryMode(request.Mode))
		if err != nil {
			response.Failed = append(response.Failed, RetryTaskFailure{ID: id, Error: err.Error()})
			continue
		}
		response.Succeeded = append(response.Succeeded, s.service.View(task))
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (s *Server) handleDeleteTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request DeleteTasksRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(request.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "at least one task id is required")
		return
	}
	response := DeleteTasksResponse{
		Deleted: make([]string, 0, len(request.IDs)),
		Failed:  make([]RetryTaskFailure, 0),
	}
	seen := make(map[string]struct{}, len(request.IDs))
	for _, rawID := range request.IDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			response.Failed = append(response.Failed, RetryTaskFailure{Error: "task id is required"})
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if err := s.service.Delete(r.Context(), id); err != nil {
			response.Failed = append(response.Failed, RetryTaskFailure{ID: id, Error: err.Error()})
			continue
		}
		response.Deleted = append(response.Deleted, id)
	}
	writeJSON(w, http.StatusAccepted, response)
}
func (s *Server) handleDeleteTasksByGID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request DeleteTasksByGIDRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(request.GIDs) == 0 {
		writeError(w, http.StatusBadRequest, "at least one GID is required")
		return
	}
	response := DeleteTasksByGIDResponse{
		Deleted:  make([]string, 0, len(request.GIDs)),
		NotFound: make([]string, 0),
		Failed:   make([]DeleteGIDFailure, 0),
	}
	seen := make(map[string]struct{}, len(request.GIDs))
	for _, rawGID := range request.GIDs {
		gid := strings.TrimSpace(rawGID)
		if gid == "" {
			response.Failed = append(response.Failed, DeleteGIDFailure{Error: "gid is required"})
			continue
		}
		if _, ok := seen[gid]; ok {
			continue
		}
		seen[gid] = struct{}{}
		if err := s.service.DeleteByGID(r.Context(), gid); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				response.NotFound = append(response.NotFound, gid)
				continue
			}
			response.Failed = append(response.Failed, DeleteGIDFailure{GID: gid, Error: err.Error()})
			continue
		}
		response.Deleted = append(response.Deleted, gid)
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (s *Server) handleTaskPath(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if len(parts) == 2 && parts[0] == "by-gid" && r.Method == http.MethodGet {
		task, err := s.service.GetByGID(parts[1])
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s.service.View(task))
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		task, err := s.service.Get(id)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s.service.View(task))
		return
	}
	if len(parts) == 1 && r.Method == http.MethodDelete {
		if err := s.service.Delete(r.Context(), id); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) == 2 && parts[1] == "retry" && r.Method == http.MethodPost {
		task, err := s.service.Retry(r.Context(), id, transfer.RetryMode(r.URL.Query().Get("mode")))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, s.service.View(task))
		return
	}
	writeError(w, http.StatusNotFound, "task route not found")
}

func (s *Server) handleCompleted(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request HookRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.service.HandleCompleted(r.Context(), request.GID, request.FilePath); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}

func (s *Server) handleStopped(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request HookRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.service.HandleStopped(request.GID, request.Reason); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "failed"})
}

func (s *Server) authorized(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	provided := strings.TrimSpace(r.Header.Get("X-API-Token"))
	if provided == "" {
		provided = strings.TrimPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ")
	}
	return len(provided) == len(s.token) && subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) == 1
}

func (s *Server) setCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	if s.allowAny {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	} else if _, ok := s.corsOrigins[origin]; ok {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Token")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
}
func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func writeServiceError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrDestinationNotFound):
		status = http.StatusNotFound
	case errors.Is(err, store.ErrAlreadyExists), errors.Is(err, store.ErrDestinationAlreadyExists), errors.Is(err, transfer.ErrDestinationInUse), errors.Is(err, transfer.ErrDefaultDestination):
		status = http.StatusConflict
	}
	writeError(w, status, err.Error())
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func appendOrigin(origins map[string]struct{}, origin string) map[string]struct{} {
	if origins == nil {
		origins = make(map[string]struct{})
	}
	origins[origin] = struct{}{}
	return origins
}
func parseTaskFilter(r *http.Request) (store.TaskFilter, error) {
	query := r.URL.Query()
	statuses := make([]string, 0)
	for _, value := range query["status"] {
		for _, status := range strings.Split(value, ",") {
			status = strings.TrimSpace(status)
			if status != "" {
				statuses = append(statuses, status)
			}
		}
	}
	filter := store.TaskFilter{
		Statuses:      statuses,
		DestinationID: query.Get("destination_id"),
		Query:         query.Get("q"),
	}
	for name, target := range map[string]*int{"limit": &filter.Limit, "offset": &filter.Offset} {
		rawValue := strings.TrimSpace(query.Get(name))
		if rawValue == "" {
			continue
		}
		value, err := strconv.Atoi(rawValue)
		if err != nil || value < 0 {
			return store.TaskFilter{}, fmt.Errorf("%s must be a non-negative integer", name)
		}
		*target = value
	}
	return filter, nil
}
