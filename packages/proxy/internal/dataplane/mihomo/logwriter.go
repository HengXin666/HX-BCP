package mihomo

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type rotatingLogWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	backups  int
	file     *os.File
	size     int64
	openedAt time.Time
}

func newRotatingLogWriter(path string, maxBytes int64, backups int) (*rotatingLogWriter, error) {
	writer := &rotatingLogWriter{path: path, maxBytes: maxBytes, backups: backups}
	if err := writer.open(); err != nil {
		return nil, err
	}
	if writer.size > writer.maxBytes {
		if err := writer.trimToTail(); err != nil {
			_ = writer.file.Close()
			return nil, err
		}
	}
	if writer.size >= writer.maxBytes {
		if err := writer.rotate(); err != nil {
			_ = writer.file.Close()
			return nil, err
		}
	}
	return writer, nil
}

func (w *rotatingLogWriter) trimToTail() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return fmt.Errorf("close oversized Mihomo log: %w", err)
		}
		w.file = nil
	}
	file, err := os.Open(w.path)
	if err != nil {
		return fmt.Errorf("open oversized Mihomo log: %w", err)
	}
	if _, err := file.Seek(-w.maxBytes, io.SeekEnd); err != nil {
		_ = file.Close()
		return fmt.Errorf("seek oversized Mihomo log: %w", err)
	}
	tail, err := io.ReadAll(io.LimitReader(file, w.maxBytes))
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("read oversized Mihomo log tail: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close oversized Mihomo log reader: %w", closeErr)
	}
	if err := os.WriteFile(w.path, tail, 0o600); err != nil {
		return fmt.Errorf("trim oversized Mihomo log: %w", err)
	}
	return w.open()
}

func (w *rotatingLogWriter) Write(content []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	originalLength := len(content)
	if originalLength == 0 {
		return 0, nil
	}
	if int64(len(content)) > w.maxBytes {
		content = content[len(content)-int(w.maxBytes):]
	}
	// Age rotation prevents a low-volume active file accumulating months of logs.
	if w.size > 0 && (w.size+int64(len(content)) > w.maxBytes || time.Since(w.openedAt) >= 24*time.Hour) {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	written, err := w.file.Write(content)
	w.size += int64(written)
	if err != nil {
		return written, err
	}
	if written != len(content) {
		return written, fmt.Errorf("short Mihomo log write: wrote %d of %d bytes", written, len(content))
	}
	return originalLength, nil
}

func (w *rotatingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rotatingLogWriter) open() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open Mihomo log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("stat Mihomo log: %w", err)
	}
	w.file = file
	w.size = info.Size()
	w.openedAt = info.ModTime()
	return nil
}

func (w *rotatingLogWriter) rotate() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return fmt.Errorf("close Mihomo log for rotation: %w", err)
		}
		w.file = nil
	}
	if w.backups == 0 {
		if err := os.Remove(w.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove Mihomo log during rotation: %w", err)
		}
	} else {
		for index := w.backups - 1; index >= 1; index-- {
			source := fmt.Sprintf("%s.%d", w.path, index)
			destination := fmt.Sprintf("%s.%d", w.path, index+1)
			if err := os.Rename(source, destination); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("rotate Mihomo log backup: %w", err)
			}
		}
		if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rotate Mihomo log: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(w.path), 0o700); err != nil {
		return fmt.Errorf("create Mihomo log directory: %w", err)
	}
	return w.open()
}
