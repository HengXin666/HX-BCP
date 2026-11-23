package overview

import "time"

type Connection struct {
	ID       string
	Upload   int64
	Download int64
}

type Snapshot struct {
	Connections []Connection
}

type Sample struct {
	Timestamp           time.Time        `json:"timestamp"`
	UploadBytesPerSec   int64            `json:"upload_bytes_per_second"`
	DownloadBytesPerSec int64            `json:"download_bytes_per_second"`
	ActiveConnections   int              `json:"active_connections"`
	Running             bool             `json:"running"`
	Resources           []ResourceSample `json:"resources,omitempty"`
	ErrorCode           string           `json:"error_code,omitempty"`
}

type ResourceSample struct {
	ResourceType        string `json:"resource_type"`
	ResourceID          string `json:"resource_id"`
	UploadBytesPerSec   int64  `json:"upload_bytes_per_second"`
	DownloadBytesPerSec int64  `json:"download_bytes_per_second"`
	ActiveConnections   int64  `json:"active_connections"`
}

func Rate(previous, current Snapshot, elapsed time.Duration) (upload, download int64) {
	if elapsed <= 0 {
		return 0, 0
	}
	seen := make(map[string]Connection, len(previous.Connections))
	for _, connection := range previous.Connections {
		if connection.ID != "" && connection.Upload >= 0 && connection.Download >= 0 {
			seen[connection.ID] = connection
		}
	}
	for _, connection := range current.Connections {
		if connection.ID == "" || connection.Upload < 0 || connection.Download < 0 {
			continue
		}
		before, exists := seen[connection.ID]
		if !exists {
			upload += connection.Upload
			download += connection.Download
			continue
		}
		if connection.Upload >= before.Upload {
			upload += connection.Upload - before.Upload
		}
		if connection.Download >= before.Download {
			download += connection.Download - before.Download
		}
	}
	seconds := elapsed.Seconds()
	return int64(float64(upload) / seconds), int64(float64(download) / seconds)
}
