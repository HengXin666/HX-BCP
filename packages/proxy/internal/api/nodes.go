package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/node"
)

func (s *Server) handleNodes(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodPost {
		var checkRequest struct {
			NodeIDs []string `json:"node_ids"`
		}
		if err := decodeJSONBody(writer, request, &checkRequest); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if request.URL.Query().Get("stream") == "1" {
			s.streamNodeChecks(writer, request, checkRequest.NodeIDs)
			return
		}
		results, err := s.nodes.CheckMany(request.Context(), checkRequest.NodeIDs)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"items": results})
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPost)
		return
	}
	limit, err := parseIntegerQuery(request, "limit", 200, 1, 1000)
	if err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	offset, err := parseIntegerQuery(request, "offset", 0, 0, 1_000_000)
	if err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	// view=summary drops the per-node health-target samples. Callers that only
	// need the inventory (member lists, pickers) must opt in: attaching health
	// samples for a page of 1000 nodes costs ~240ms against a 2750-node
	// inventory, and none of them render the field. The default stays the full
	// projection so existing consumers cannot silently lose data.
	view := strings.TrimSpace(request.URL.Query().Get("view"))
	switch view {
	case "", "full", "summary":
	default:
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "view must be full or summary")
		return
	}
	filter := node.Filter{
		Search:           request.URL.Query().Get("search"),
		Protocol:         request.URL.Query().Get("protocol"),
		State:            request.URL.Query().Get("state"),
		Limit:            limit,
		Offset:           offset,
		SkipHealthChecks: view == "summary",
	}
	items, err := s.nodes.List(request.Context(), filter)
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	response := map[string]any{
		"items":  items,
		"limit":  limit,
		"offset": offset,
	}
	// with_total=1 answers the size of the filtered set so a client can fetch
	// the remaining pages concurrently. It is opt-in because it costs an extra
	// COUNT on a collection that can hold thousands of rows.
	if request.URL.Query().Get("with_total") == "1" {
		total, countErr := s.nodes.Count(request.Context(), filter)
		if countErr != nil {
			s.handleError(writer, request, countErr)
			return
		}
		response["total"] = total
	}
	writeJSON(writer, http.StatusOK, response)
}

func (s *Server) streamNodeChecks(writer http.ResponseWriter, request *http.Request, ids []string) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		s.writeAPIError(writer, request, http.StatusInternalServerError, "stream_unavailable", "streaming is unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/x-ndjson")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	encoder := json.NewEncoder(writer)
	started := false
	err := s.nodes.CheckManyProgress(request.Context(), ids, func(progress node.CheckProgress) error {
		if !started {
			writer.WriteHeader(http.StatusOK)
			started = true
		}
		if err := encoder.Encode(progress); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err != nil && !started {
		s.handleError(writer, request, err)
	}
}

func (s *Server) handleNodeSettings(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		settings, err := s.nodes.QualitySettings(request.Context())
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, settings)
	case http.MethodPut:
		var body node.QualitySettings
		if err := decodeJSONBody(writer, request, &body); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		settings, err := s.nodes.UpdateQualitySettings(request.Context(), body)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, settings)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPut)
	}
}

func (s *Server) handleNode(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/api/v1/nodes/")
	if path == "" || path == request.URL.Path {
		http.NotFound(writer, request)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "check" {
		if request.Method != http.MethodPost {
			methodNotAllowed(writer, request, http.MethodPost)
			return
		}
		result, err := s.nodes.Check(request.Context(), parts[0])
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, result)
		return
	}
	if len(parts) == 2 && (parts[1] == "disable" || parts[1] == "enable") {
		if request.Method != http.MethodPost {
			methodNotAllowed(writer, request, http.MethodPost)
			return
		}
		var (
			item node.Node
			err  error
		)
		if parts[1] == "disable" {
			item, err = s.nodes.Disable(request.Context(), parts[0])
		} else {
			item, err = s.nodes.Enable(request.Context(), parts[0])
		}
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, item)
		return
	}
	if len(parts) != 1 || request.Method != http.MethodGet {
		if len(parts) == 1 {
			methodNotAllowed(writer, request, http.MethodGet)
		} else {
			http.NotFound(writer, request)
		}
		return
	}
	item, err := s.nodes.Get(request.Context(), parts[0])
	if errors.Is(err, node.ErrNotFound) {
		s.writeAPIError(writer, request, http.StatusNotFound, "node_not_found", "node not found")
		return
	}
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, item)
}
