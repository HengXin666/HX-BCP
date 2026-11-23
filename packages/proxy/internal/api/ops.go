package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OpsEndpoints are the read-only host-ops surfaces (disk + docker) added for
// the terminal page's ops sub-tabs (20260823). They reuse the terminal
// service's privileged remote-exec path with fixed command strings — there is
// no user-supplied input, so the commands are not an injection surface.
//
// Security model mirrors /api/v1/system/resources: the endpoints expose
// utilization/listing numbers, not shell authority, so the normal session/API
// key auth applies without a 2FA step-up.

const opsExecTimeout = 20 * time.Second

// DiskUsage is one mounted filesystem row from `df -B1 -P`.
type DiskUsage struct {
	Filesystem string `json:"filesystem"`
	SizeBytes  uint64 `json:"size_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
	AvailBytes uint64 `json:"avail_bytes"`
	UsePercent uint64 `json:"use_percent"`
	MountedOn  string `json:"mounted_on"`
}

// handleSystemDisk runs `df -B1 -P` on the host and returns a structured list
// of mounted filesystems. Only filesystems with real capacity are included;
// pseudo filesystems (proc/sysfs/tmpfs/devtmpfs/cgroup etc.) are filtered out.
func (s *Server) handleSystemDisk(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	if s.terminal == nil {
		s.writeAPIError(writer, request, http.StatusNotFound, "ops_unavailable", "terminal remote-exec is not configured")
		return
	}
	result, err := s.terminal.Execute(request.Context(), "df -B1 -P", opsExecTimeout)
	if err != nil {
		s.writeAPIError(writer, request, http.StatusServiceUnavailable, "ops_disk_failed", err.Error())
		return
	}
	if result.ExitCode != 0 {
		s.writeAPIError(writer, request, http.StatusServiceUnavailable, "ops_disk_failed", strings.TrimSpace(result.Stderr))
		return
	}
	rows, err := parseDfOutput(result.Stdout)
	if err != nil {
		s.writeAPIError(writer, request, http.StatusInternalServerError, "ops_disk_parse_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"filesystems": rows})
}

// parseDfOutput converts the `df -B1 -P` text table into structured rows.
// Header line is skipped; lines are space-separated with exactly 6 columns:
// Filesystem 1024-blocks Used Available Capacity Mounted-on.
func parseDfOutput(output string) ([]DiskUsage, error) {
	rows := make([]DiskUsage, 0, 8)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Filesystem ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		size, err1 := strconv.ParseUint(fields[1], 10, 64)
		used, err2 := strconv.ParseUint(fields[2], 10, 64)
		avail, err3 := strconv.ParseUint(fields[3], 10, 64)
		usePct, err4 := strconv.ParseUint(strings.TrimSuffix(fields[4], "%"), 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		mount := strings.Join(fields[5:], " ")
		// Filter pseudo/zero-capacity filesystems so the ops page shows real
		// disks (overlay, proc, sysfs, tmpfs, devtmpfs, cgroup, shm, mqueue).
		if size == 0 || isPseudoFilesystem(fields[0]) {
			continue
		}
		rows = append(rows, DiskUsage{
			Filesystem: fields[0],
			SizeBytes:  size,
			UsedBytes:  used,
			AvailBytes: avail,
			UsePercent: usePct,
			MountedOn:  mount,
		})
	}
	return rows, nil
}

func isPseudoFilesystem(source string) bool {
	lower := strings.ToLower(source)
	switch {
	case strings.HasPrefix(lower, "overlay"),
		strings.HasPrefix(lower, "tmpfs"),
		strings.HasPrefix(lower, "devtmpfs"),
		strings.HasPrefix(lower, "proc"),
		strings.HasPrefix(lower, "sysfs"),
		strings.HasPrefix(lower, "cgroup"),
		strings.HasPrefix(lower, "shm"),
		strings.HasPrefix(lower, "mqueue"),
		strings.HasPrefix(lower, "none"),
		strings.HasPrefix(lower, "udev"):
		return true
	}
	return false
}

// DockerContainer is one row of `docker ps -a` merged with `docker stats`
// where the container is running.
type DockerContainer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Image   string `json:"image"`
	State   string `json:"state"`
	Status  string `json:"status"`
	Ports   string `json:"ports"`
	Created string `json:"created"`
	// Runtime stats are only present for running containers.
	CPUPerc  string `json:"cpu_perc,omitempty"`
	MemUsage string `json:"mem_usage,omitempty"`
	MemPerc  string `json:"mem_perc,omitempty"`
	NetIO    string `json:"net_io,omitempty"`
	BlockIO  string `json:"block_io,omitempty"`
}

// handleDockerContainers lists all containers via `docker ps -a` and merges
// live `docker stats --no-stream` metrics for the running ones. Read-only:
// no start/stop/restart/remove operations are exposed.
func (s *Server) handleDockerContainers(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	if s.terminal == nil {
		s.writeAPIError(writer, request, http.StatusNotFound, "ops_unavailable", "terminal remote-exec is not configured")
		return
	}
	psResult, err := s.terminal.Execute(request.Context(), `docker ps -a --no-trunc --format '{{json .}}'`, opsExecTimeout)
	if err != nil {
		s.writeAPIError(writer, request, http.StatusServiceUnavailable, "ops_docker_failed", err.Error())
		return
	}
	if psResult.ExitCode != 0 {
		s.writeAPIError(writer, request, http.StatusServiceUnavailable, "ops_docker_failed", strings.TrimSpace(psResult.Stderr))
		return
	}
	containers, err := parseDockerPS(psResult.Stdout)
	if err != nil {
		s.writeAPIError(writer, request, http.StatusInternalServerError, "ops_docker_parse_failed", err.Error())
		return
	}

	// Only query docker stats when there is at least one running container; an
	// empty `docker stats` invocation would otherwise stall for the sampling
	// window and return nothing useful.
	if len(containers) > 0 {
		statsResult, statsErr := s.terminal.Execute(request.Context(), `docker stats --no-stream --format '{{json .}}'`, opsExecTimeout)
		if statsErr == nil && statsResult.ExitCode == 0 {
			mergeDockerStats(containers, statsResult.Stdout)
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"containers": containers})
}

type dockerPSRow struct {
	ID      string `json:"ID"`
	Names   string `json:"Names"`
	Image   string `json:"Image"`
	State   string `json:"State"`
	Status  string `json:"Status"`
	Ports   string `json:"Ports"`
	Created string `json:"CreatedAt"`
}

type dockerStatsRow struct {
	Container string `json:"Container"`
	CPUPerc   string `json:"CPUPerc"`
	MemUsage  string `json:"MemUsage"`
	MemPerc   string `json:"MemPerc"`
	NetIO     string `json:"NetIO"`
	BlockIO   string `json:"BlockIO"`
}

func parseDockerPS(output string) ([]DockerContainer, error) {
	rows := make([]DockerContainer, 0, 16)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row dockerPSRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			// A malformed line (e.g. a WAF page or truncated output) should not
			// fail the whole list; skip it.
			continue
		}
		rows = append(rows, DockerContainer{
			ID:      row.ID,
			Name:    row.Names,
			Image:   row.Image,
			State:   row.State,
			Status:  row.Status,
			Ports:   row.Ports,
			Created: row.Created,
		})
	}
	return rows, nil
}

func mergeDockerStats(containers []DockerContainer, output string) {
	byID := make(map[string]DockerContainer, len(containers))
	for _, container := range containers {
		byID[container.ID] = container
	}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row dockerStatsRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		current, ok := byID[row.Container]
		if !ok {
			continue
		}
		current.CPUPerc = row.CPUPerc
		current.MemUsage = row.MemUsage
		current.MemPerc = row.MemPerc
		current.NetIO = row.NetIO
		current.BlockIO = row.BlockIO
		byID[row.Container] = current
	}
	for index := range containers {
		containers[index] = byID[containers[index].ID]
	}
}
