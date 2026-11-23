package api

import "net/http"

// handleTerminalFiles dispatches GET (list), and
// path=/api/v1/terminal/files?path=... is used for stat/download via query.
// Upload and download use separate query keys handled here for clarity.
func (s *Server) handleTerminalFiles(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Query().Get("op") {
	case "stat":
		s.handleTerminalFileStat(writer, request)
	case "download":
		s.handleTerminalFileDownload(writer, request)
	case "upload":
		// POST handled separately below for clarity.
		s.handleTerminalFileUpload(writer, request)
	default:
		if request.Method == http.MethodPost {
			s.handleTerminalFileUpload(writer, request)
			return
		}
		s.handleTerminalFileList(writer, request)
	}
}
