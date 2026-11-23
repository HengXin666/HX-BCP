package terminal

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// helperFileResult is the JSON payload of every file-operation response.
// Entries carries the listing for frameFileList, Size/Mode/IsDir/Modified
// the metadata for stat and download headers.
type helperFileResult struct {
	OK       bool        `json:"ok"`
	Code     string      `json:"code,omitempty"`
	Message  string      `json:"message,omitempty"`
	Entries  []FileEntry `json:"entries,omitempty"`
	Size     int64       `json:"size,omitempty"`
	Mode     string      `json:"mode,omitempty"`
	IsDir    bool        `json:"is_dir,omitempty"`
	Modified time.Time   `json:"modified,omitempty"`
}

// helperPath validates that a file-operation target is an absolute path with
// no NUL bytes. The control plane already rejects relative paths, but the
// helper must never rely on the caller's validation: it runs privileged.
func helperPath(raw string) (string, error) {
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

// helperFileError classifies filesystem errors into the same stable codes the
// API layer uses, so the control plane can map them onto its own codes.
func helperFileError(err error) (code, message string) {
	if errors.Is(err, os.ErrPermission) {
		return "forbidden", "permission denied"
	}
	if errors.Is(err, os.ErrNotExist) {
		return "not_found", "path does not exist"
	}
	return "failed", err.Error()
}

func writeFileResult(connection net.Conn, result helperFileResult) bool {
	payload, err := json.Marshal(result)
	if err != nil {
		return false
	}
	return writeFrame(connection, frameFileResult, payload) == nil
}

// handleHelperFileRequest serves one file operation per connection. The
// privileged socket is the only root-capable file path for the terminal file
// manager, so every operation re-validates the target path and bounds sizes.
func handleHelperFileRequest(connection net.Conn, kind byte, payload []byte) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(helperFileIdleTimeout))
	switch kind {
	case frameFileList:
		handleFileList(connection, string(payload))
	case frameFileStat:
		handleFileStat(connection, string(payload))
	case frameFileDownload:
		handleFileDownload(connection, string(payload))
	case frameFileUpload:
		handleFileUpload(connection, payload)
	case frameFileMkdir:
		handleFileMkdir(connection, string(payload))
	case frameFileRemove:
		handleFileRemove(connection, string(payload))
	}
}

func handleFileList(connection net.Conn, raw string) {
	path, err := helperPath(raw)
	if err != nil {
		writeFileResult(connection, helperFileResult{Code: "failed", Message: err.Error()})
		return
	}
	dir, err := os.Open(path)
	if err != nil {
		code, message := helperFileError(err)
		writeFileResult(connection, helperFileResult{Code: code, Message: message})
		return
	}
	defer dir.Close()
	names, err := dir.Readdirnames(0)
	if err != nil {
		code, message := helperFileError(err)
		writeFileResult(connection, helperFileResult{Code: code, Message: message})
		return
	}
	if len(names) > MaxFileListEntries {
		names = names[:MaxFileListEntries]
	}
	entries := make([]FileEntry, 0, len(names))
	for _, name := range names {
		full := filepath.Join(path, name)
		info, statErr := os.Lstat(full)
		if statErr != nil {
			continue
		}
		entries = append(entries, FileEntry{
			Name:     name,
			Size:     info.Size(),
			Mode:     info.Mode().String(),
			IsDir:    info.IsDir(),
			Modified: info.ModTime().UTC(),
		})
	}
	sortFileEntries(entries)
	writeFileResult(connection, helperFileResult{OK: true, Entries: entries})
}

func handleFileStat(connection net.Conn, raw string) {
	path, err := helperPath(raw)
	if err != nil {
		writeFileResult(connection, helperFileResult{Code: "failed", Message: err.Error()})
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		code, message := helperFileError(err)
		writeFileResult(connection, helperFileResult{Code: code, Message: message})
		return
	}
	writeFileResult(connection, helperFileResult{
		OK:       true,
		Size:     info.Size(),
		Mode:     info.Mode().String(),
		IsDir:    info.IsDir(),
		Modified: info.ModTime().UTC(),
	})
}

func handleFileDownload(connection net.Conn, raw string) {
	path, err := helperPath(raw)
	if err != nil {
		writeFileResult(connection, helperFileResult{Code: "failed", Message: err.Error()})
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		code, message := helperFileError(err)
		writeFileResult(connection, helperFileResult{Code: code, Message: message})
		return
	}
	if info.IsDir() {
		writeFileResult(connection, helperFileResult{Code: "failed", Message: "path is a directory"})
		return
	}
	file, err := os.Open(path)
	if err != nil {
		code, message := helperFileError(err)
		writeFileResult(connection, helperFileResult{Code: code, Message: message})
		return
	}
	defer file.Close()
	if !writeFileResult(connection, helperFileResult{
		OK:       true,
		Size:     info.Size(),
		Mode:     info.Mode().String(),
		IsDir:    false,
		Modified: info.ModTime().UTC(),
	}) {
		return
	}
	buffer := make([]byte, helperFileChunk)
	for {
		count, readErr := file.Read(buffer)
		if count > 0 {
			if writeFrame(connection, frameFileData, buffer[:count]) != nil {
				return
			}
		}
		if readErr != nil {
			return
		}
	}
}

type helperUploadRequest struct {
	Dir  string `json:"dir"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func handleFileUpload(connection net.Conn, payload []byte) {
	var request helperUploadRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		writeFileResult(connection, helperFileResult{Code: "failed", Message: "invalid upload request"})
		return
	}
	dir, err := helperPath(request.Dir)
	if err != nil {
		writeFileResult(connection, helperFileResult{Code: "failed", Message: err.Error()})
		return
	}
	name := filepath.Base(filepath.Clean(request.Name))
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, 0) ||
		strings.ContainsAny(name, string(filepath.Separator)) {
		writeFileResult(connection, helperFileResult{Code: "failed", Message: "invalid filename"})
		return
	}
	if request.Size < 0 || request.Size > MaxUploadBytes {
		writeFileResult(connection, helperFileResult{Code: "failed", Message: "upload size out of range"})
		return
	}
	if !writeFileResult(connection, helperFileResult{OK: true}) {
		return
	}
	destination := filepath.Join(dir, name)
	tmp := destination + ".hxpart"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		code, message := helperFileError(err)
		writeFileResult(connection, helperFileResult{Code: code, Message: message})
		return
	}
	fail := func(result helperFileResult) {
		_ = out.Close()
		_ = os.Remove(tmp)
		writeFileResult(connection, result)
	}
	var received int64
	for received < request.Size {
		kind, chunk, readErr := readFrame(connection)
		if readErr != nil {
			fail(helperFileResult{Code: "failed", Message: "upload stream interrupted"})
			return
		}
		if kind != frameFileData {
			fail(helperFileResult{Code: "failed", Message: "unexpected upload frame"})
			return
		}
		if received+int64(len(chunk)) > request.Size {
			fail(helperFileResult{Code: "failed", Message: "upload exceeded declared size"})
			return
		}
		if _, writeErr := out.Write(chunk); writeErr != nil {
			code, message := helperFileError(writeErr)
			fail(helperFileResult{Code: code, Message: message})
			return
		}
		received += int64(len(chunk))
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		code, message := helperFileError(err)
		writeFileResult(connection, helperFileResult{Code: code, Message: message})
		return
	}
	if err := os.Rename(tmp, destination); err != nil {
		_ = os.Remove(tmp)
		code, message := helperFileError(err)
		writeFileResult(connection, helperFileResult{Code: code, Message: message})
		return
	}
	writeFileResult(connection, helperFileResult{OK: true, Size: request.Size})
}

func handleFileMkdir(connection net.Conn, raw string) {
	path, err := helperPath(raw)
	if err != nil {
		writeFileResult(connection, helperFileResult{Code: "failed", Message: err.Error()})
		return
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		code, message := helperFileError(err)
		writeFileResult(connection, helperFileResult{Code: code, Message: message})
		return
	}
	writeFileResult(connection, helperFileResult{OK: true})
}

func handleFileRemove(connection net.Conn, raw string) {
	path, err := helperPath(raw)
	if err != nil {
		writeFileResult(connection, helperFileResult{Code: "failed", Message: err.Error()})
		return
	}
	if err := os.Remove(path); err != nil {
		code, message := helperFileError(err)
		writeFileResult(connection, helperFileResult{Code: code, Message: message})
		return
	}
	writeFileResult(connection, helperFileResult{OK: true})
}

// sortFileEntries orders directories first, then names, matching the API
// contract so listings stay deterministic regardless of readdir order.
func sortFileEntries(entries []FileEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})
}
