package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileEntry is the API view of one filesystem entry. The helper and the local
// backend produce the same structure so the file manager behaves identically
// whether it is served by the privileged root helper or the control plane.
type FileEntry struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Mode     string    `json:"mode"`
	IsDir    bool      `json:"is_dir"`
	Modified time.Time `json:"modified"`
}

// FileError carries a stable classification code so the API layer can map it
// onto its own error codes without parsing message text.
type FileError struct {
	Code    string
	Message string
}

func (e *FileError) Error() string { return e.Message }

// fileError maps a filesystem error to a classified FileError.
func fileError(err error) *FileError {
	if err == nil {
		return &FileError{Code: "failed", Message: ""}
	}
	var known *FileError
	if errors.As(err, &known) {
		return known
	}
	switch {
	case errors.Is(err, os.ErrPermission):
		return &FileError{Code: "forbidden", Message: "permission denied"}
	case errors.Is(err, os.ErrNotExist):
		return &FileError{Code: "not_found", Message: "path does not exist"}
	default:
		return &FileError{Code: "failed", Message: err.Error()}
	}
}

func (s *Service) privilegedSocket() string { return strings.TrimSpace(s.config.PrivilegedSocket) }

// ListFiles returns one directory listing with the same privilege domain as
// the terminal shell: via the root PTY helper when one is configured,
// otherwise as the control-plane user (run.sh local mode).
func (s *Service) ListFiles(ctx context.Context, path string) ([]FileEntry, error) {
	if socket := s.privilegedSocket(); socket != "" {
		return remoteFileList(ctx, socket, path)
	}
	return localFileList(path)
}

// StatFile returns metadata for one path.
func (s *Service) StatFile(ctx context.Context, path string) (FileEntry, error) {
	if socket := s.privilegedSocket(); socket != "" {
		return remoteFileStat(ctx, socket, path)
	}
	return localFileStat(path)
}

// DownloadFile streams one file into writer and returns the number of bytes.
func (s *Service) DownloadFile(ctx context.Context, path string, writer io.Writer) (int64, error) {
	if socket := s.privilegedSocket(); socket != "" {
		return remoteFileDownload(ctx, socket, path, writer)
	}
	return localFileDownload(path, writer)
}

// UploadFile streams size bytes from reader into dir/name.
func (s *Service) UploadFile(ctx context.Context, dir, name string, reader io.Reader, size int64) error {
	if socket := s.privilegedSocket(); socket != "" {
		return remoteFileUpload(ctx, socket, dir, name, reader, size)
	}
	return localFileUpload(dir, name, reader, size)
}

// Mkdir creates a directory (with parents).
func (s *Service) Mkdir(ctx context.Context, path string) error {
	if socket := s.privilegedSocket(); socket != "" {
		return remoteFileMkdir(ctx, socket, path)
	}
	return localFileMkdir(path)
}

// RemoveFile deletes one file or empty directory.
func (s *Service) RemoveFile(ctx context.Context, path string) error {
	if socket := s.privilegedSocket(); socket != "" {
		return remoteFileRemove(ctx, socket, path)
	}
	return localFileRemove(path)
}

// --- local implementations (control-plane user, run.sh mode) ---

func localFileList(path string) ([]FileEntry, error) {
	dir, err := os.Open(path)
	if err != nil {
		return nil, fileError(err)
	}
	defer dir.Close()
	names, err := dir.Readdirnames(0)
	if err != nil {
		return nil, fileError(err)
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
	return entries, nil
}

func localFileStat(path string) (FileEntry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileEntry{}, fileError(err)
	}
	return FileEntry{
		Name:     filepath.Base(path),
		Size:     info.Size(),
		Mode:     info.Mode().String(),
		IsDir:    info.IsDir(),
		Modified: info.ModTime().UTC(),
	}, nil
}

func localFileDownload(path string, writer io.Writer) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fileError(err)
	}
	defer file.Close()
	count, err := io.Copy(writer, file)
	if err != nil {
		return count, fileError(err)
	}
	return count, nil
}

func localFileUpload(dir, name string, reader io.Reader, size int64) error {
	if size < 0 || size > MaxUploadBytes {
		return fileError(fmt.Errorf("upload size out of range"))
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fileError(err)
	}
	if !info.IsDir() {
		return &FileError{Code: "failed", Message: "path must be a directory"}
	}
	destination := filepath.Join(dir, name)
	tmp := destination + ".hxpart"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fileError(err)
	}
	written, copyErr := io.CopyN(out, reader, size)
	if copyErr != nil && written != size {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fileError(copyErr)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fileError(err)
	}
	if err := os.Rename(tmp, destination); err != nil {
		_ = os.Remove(tmp)
		return fileError(err)
	}
	return nil
}

func localFileMkdir(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fileError(err)
	}
	return nil
}

func localFileRemove(path string) error {
	if err := os.Remove(path); err != nil {
		return fileError(err)
	}
	return nil
}

// --- privileged helper implementations ---

func dialHelper(ctx context.Context, socket string) (net.Conn, error) {
	dialContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(dialContext, "unix", socket)
	if err != nil {
		return nil, fileError(fmt.Errorf("connect terminal helper: %w", err))
	}
	if err := connection.SetDeadline(time.Now().Add(helperFileIdleTimeout)); err != nil {
		_ = connection.Close()
		return nil, fileError(fmt.Errorf("set terminal helper deadline: %w", err))
	}
	return connection, nil
}

// readFileResult reads one response frame and classifies failures.
func readFileResult(connection net.Conn) (*helperFileResult, error) {
	kind, payload, err := readFrame(connection)
	if err != nil {
		return nil, fileError(fmt.Errorf("read helper file response: %w", err))
	}
	if kind != frameFileResult {
		return nil, &FileError{Code: "failed", Message: fmt.Sprintf("unexpected helper response frame %d", kind)}
	}
	var result helperFileResult
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, fileError(fmt.Errorf("decode helper file response: %w", err))
	}
	if !result.OK {
		return nil, &FileError{Code: result.Code, Message: result.Message}
	}
	return &result, nil
}

func remoteFileList(ctx context.Context, socket, path string) ([]FileEntry, error) {
	connection, err := dialHelper(ctx, socket)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	if err := writeFrame(connection, frameFileList, []byte(path)); err != nil {
		return nil, fileError(err)
	}
	result, err := readFileResult(connection)
	if err != nil {
		return nil, err
	}
	return result.Entries, nil
}

func remoteFileStat(ctx context.Context, socket, path string) (FileEntry, error) {
	connection, err := dialHelper(ctx, socket)
	if err != nil {
		return FileEntry{}, err
	}
	defer connection.Close()
	if err := writeFrame(connection, frameFileStat, []byte(path)); err != nil {
		return FileEntry{}, fileError(err)
	}
	result, err := readFileResult(connection)
	if err != nil {
		return FileEntry{}, err
	}
	return FileEntry{
		Name:     filepath.Base(path),
		Size:     result.Size,
		Mode:     result.Mode,
		IsDir:    result.IsDir,
		Modified: result.Modified,
	}, nil
}

func remoteFileDownload(ctx context.Context, socket, path string, writer io.Writer) (int64, error) {
	connection, err := dialHelper(ctx, socket)
	if err != nil {
		return 0, err
	}
	defer connection.Close()
	if err := writeFrame(connection, frameFileDownload, []byte(path)); err != nil {
		return 0, fileError(err)
	}
	result, err := readFileResult(connection)
	if err != nil {
		return 0, err
	}
	size := result.Size
	var written int64
	for written < size {
		kind, payload, readErr := readFrame(connection)
		if readErr != nil {
			return written, fileError(fmt.Errorf("download stream interrupted: %w", readErr))
		}
		if kind == frameFileResult {
			var failed helperFileResult
			if json.Unmarshal(payload, &failed) == nil && !failed.OK {
				return written, &FileError{Code: failed.Code, Message: failed.Message}
			}
			return written, &FileError{Code: "failed", Message: "unexpected download response"}
		}
		if kind != frameFileData {
			return written, &FileError{Code: "failed", Message: fmt.Sprintf("unexpected download frame %d", kind)}
		}
		count, writeErr := writer.Write(payload)
		written += int64(count)
		if writeErr != nil {
			return written, fileError(writeErr)
		}
	}
	return written, nil
}

func remoteFileUpload(ctx context.Context, socket, dir, name string, reader io.Reader, size int64) error {
	connection, err := dialHelper(ctx, socket)
	if err != nil {
		return err
	}
	defer connection.Close()
	request, err := json.Marshal(helperUploadRequest{Dir: dir, Name: name, Size: size})
	if err != nil {
		return fileError(err)
	}
	if err := writeFrame(connection, frameFileUpload, request); err != nil {
		return fileError(err)
	}
	if _, err := readFileResult(connection); err != nil {
		return err // helper refused before any data was sent
	}
	buffer := make([]byte, helperFileChunk)
	var sent int64
	for sent < size {
		count, readErr := reader.Read(buffer)
		if count > 0 {
			if writeErr := writeFrame(connection, frameFileData, buffer[:count]); writeErr != nil {
				return fileError(writeErr)
			}
			sent += int64(count)
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return fileError(readErr)
		}
	}
	_, err = readFileResult(connection)
	return err
}

func remoteFileMkdir(ctx context.Context, socket, path string) error {
	connection, err := dialHelper(ctx, socket)
	if err != nil {
		return err
	}
	defer connection.Close()
	if err := writeFrame(connection, frameFileMkdir, []byte(path)); err != nil {
		return fileError(err)
	}
	_, err = readFileResult(connection)
	return err
}

func remoteFileRemove(ctx context.Context, socket, path string) error {
	connection, err := dialHelper(ctx, socket)
	if err != nil {
		return err
	}
	defer connection.Close()
	if err := writeFrame(connection, frameFileRemove, []byte(path)); err != nil {
		return fileError(err)
	}
	_, err = readFileResult(connection)
	return err
}
