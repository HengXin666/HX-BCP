package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/terminal"
)

type terminalFileListResponse struct {
	Path    string               `json:"path"`
	Parent  string               `json:"parent"`
	Entries []terminal.FileEntry `json:"entries"`
}

// resolveTerminalPath validates and normalizes a path argument. It must be
// absolute so the file manager never operates on paths relative to the control
// plane's working directory by accident.
func resolveTerminalPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("path is required")
	}
	if strings.ContainsRune(raw, 0) {
		return "", errors.New("invalid path")
	}
	cleaned := filepath.Clean(raw)
	if !filepath.IsAbs(cleaned) {
		return "", errors.New("path must be absolute")
	}
	return cleaned, nil
}

// terminalFileError maps a terminal-service failure to a stable API code and
// the most accurate HTTP status for one file-manager operation. The frontend
// branches on the code, never on the message text: missing and forbidden paths
// are client-side conditions the file panel can render inline, while
// everything else stays an unexpected server-side failure.
func terminalFileError(operation string, err error) (code string, status int) {
	var fileErr *terminal.FileError
	if errors.As(err, &fileErr) {
		switch fileErr.Code {
		case "forbidden":
			return operation + "_forbidden", http.StatusForbidden
		case "not_found":
			return operation + "_not_found", http.StatusNotFound
		}
	}
	return operation + "_failed", http.StatusBadRequest
}

func terminalFileMessage(err error) string {
	if err == nil {
		return ""
	}
	var fileErr *terminal.FileError
	if errors.As(err, &fileErr) {
		return fileErr.Message
	}
	return err.Error()
}

// handleTerminalFileList lists one directory. The result is bounded by the
// terminal service and entries are sorted (directories first).
func (s *Server) handleTerminalFileList(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	raw := request.URL.Query().Get("path")
	path, err := resolveTerminalPath(raw)
	if err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	entries, err := s.terminal.ListFiles(request.Context(), path)
	if err != nil {
		code, status := terminalFileError("file_list", err)
		s.writeAPIError(writer, request, status, code, terminalFileMessage(err))
		return
	}
	parent := filepath.Dir(path)
	if parent == path {
		parent = ""
	}
	writeJSON(writer, http.StatusOK, terminalFileListResponse{Path: path, Parent: parent, Entries: entries})
}

// handleTerminalFileDownload streams one file. Large downloads are not buffered
// in memory; size is announced so the browser can show progress.
func (s *Server) handleTerminalFileDownload(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	path, err := resolveTerminalPath(request.URL.Query().Get("path"))
	if err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	info, err := s.terminal.StatFile(request.Context(), path)
	if err != nil {
		code, status := terminalFileError("file_download", err)
		s.writeAPIError(writer, request, status, code, terminalFileMessage(err))
		return
	}
	if info.IsDir {
		s.writeAPIError(writer, request, http.StatusBadRequest, "validation_failed", "path is a directory")
		return
	}
	writer.Header().Set("Content-Type", "application/octet-stream")
	encoded := filepath.Base(path)
	if encoded == "" || encoded == "." || encoded == string(filepath.Separator) {
		encoded = "download"
	}
	writer.Header().Set("Content-Disposition", "attachment; filename=\""+encoded+"\"")
	writer.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	if _, err := s.terminal.DownloadFile(request.Context(), path, writer); err != nil {
		// Headers are already sent; the browser sees a truncated download.
		s.logger.Warn("terminal file download failed", "path", path, "error", err)
	}
}

// handleTerminalFileUpload accepts a multipart upload writing into the
// provided directory. The total upload is bounded by the terminal service.
func (s *Server) handleTerminalFileUpload(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	dir, err := resolveTerminalPath(request.URL.Query().Get("path"))
	if err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	info, err := s.terminal.StatFile(request.Context(), dir)
	if err != nil {
		code, status := terminalFileError("file_upload", err)
		s.writeAPIError(writer, request, status, code, terminalFileMessage(err))
		return
	}
	if !info.IsDir {
		s.writeAPIError(writer, request, http.StatusBadRequest, "validation_failed", "path must be a directory")
		return
	}
	// Cap request size at the decoder level so a giant upload is refused early.
	request.Body = http.MaxBytesReader(writer, request.Body, terminal.MaxUploadBytes+1<<20)
	if err := request.ParseMultipartForm(terminal.MaxUploadBytes); err != nil {
		s.writeAPIError(writer, request, http.StatusRequestEntityTooLarge, "upload_too_large", errorText(err))
		return
	}
	form := request.MultipartForm
	if form == nil || len(form.File) == 0 {
		s.writeAPIError(writer, request, http.StatusBadRequest, "validation_failed", "no files in upload")
		return
	}
	saved := make([]string, 0, len(form.File))
	for field := range form.File {
		for _, header := range form.File[field] {
			name := filepath.Base(filepath.Clean(header.Filename))
			if name == "" || name == "." || name == ".." || strings.ContainsRune(name, 0) || strings.ContainsAny(name, string(filepath.Separator)) {
				s.writeAPIError(writer, request, http.StatusBadRequest, "validation_failed", "invalid filename: "+header.Filename)
				return
			}
			src, err := header.Open()
			if err != nil {
				code, status := terminalFileError("file_upload", err)
				s.writeAPIError(writer, request, status, code, terminalFileMessage(err))
				return
			}
			uploadErr := s.terminal.UploadFile(request.Context(), dir, name, src, header.Size)
			_ = src.Close()
			if uploadErr != nil {
				code, status := terminalFileError("file_upload", uploadErr)
				s.writeAPIError(writer, request, status, code, terminalFileMessage(uploadErr))
				return
			}
			saved = append(saved, name)
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"saved": saved})
}

// handleTerminalFileStat returns metadata (size / mode) for a single path,
// used by the download action to confirm a file before transfer.
func (s *Server) handleTerminalFileStat(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	path, err := resolveTerminalPath(request.URL.Query().Get("path"))
	if err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	info, err := s.terminal.StatFile(request.Context(), path)
	if err != nil {
		code, status := terminalFileError("file_stat", err)
		s.writeAPIError(writer, request, status, code, terminalFileMessage(err))
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"path":     path,
		"size":     info.Size,
		"is_dir":   info.IsDir,
		"mode":     info.Mode,
		"modified": info.Modified.UTC().Format(time.RFC3339),
	})
}

// handleTerminalFileMkdir creates a directory at the given path.
func (s *Server) handleTerminalFileMkdir(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	path, err := resolveTerminalPath(body.Path)
	if err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	if err := s.terminal.Mkdir(request.Context(), path); err != nil {
		code, status := terminalFileError("mkdir", err)
		s.writeAPIError(writer, request, status, code, terminalFileMessage(err))
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"path": path})
}

// handleTerminalFileRemove deletes a file or empty directory.
func (s *Server) handleTerminalFileRemove(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	path, err := resolveTerminalPath(body.Path)
	if err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	if err := s.terminal.RemoveFile(request.Context(), path); err != nil {
		code, status := terminalFileError("remove", err)
		s.writeAPIError(writer, request, status, code, terminalFileMessage(err))
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"removed": path})
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	var fileErr *terminal.FileError
	if errors.As(err, &fileErr) {
		return fileErr.Message
	}
	return err.Error()
}
