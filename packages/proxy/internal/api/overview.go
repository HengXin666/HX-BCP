package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/HengXin666/hx-pxy/internal/dataplane/mihomo"
	"github.com/HengXin666/hx-pxy/internal/metrics"
	"github.com/HengXin666/hx-pxy/internal/overview"
)

func (s *Server) handleOverviewStream(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		s.writeAPIError(writer, request, http.StatusInternalServerError, "stream_unsupported", "streaming is not supported")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache, no-store")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	if liveTraffic, ok := s.traffic.(TrafficLiveService); ok {
		s.handleLiveOverviewStream(writer, request, flusher, liveTraffic)
		return
	}

	previous, previousAt, _ := s.readOverviewSnapshot(request, time.Now().UTC())
	initial := overview.Sample{
		Timestamp: previousAt, ActiveConnections: len(previous.Connections), Running: s.overview.Status().Running,
	}
	if err := writeOverviewSample(writer, initial); err != nil {
		return
	}
	flusher.Flush()

	ticker := time.NewTicker(s.overviewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case tick := <-ticker.C:
			current, sampledAt, sampleErr := s.readOverviewSnapshot(request, tick.UTC())
			sample := overview.Sample{
				Timestamp: sampledAt, ActiveConnections: len(current.Connections), Running: s.overview.Status().Running,
			}
			if sampleErr == nil {
				sample.UploadBytesPerSec, sample.DownloadBytesPerSec = overview.Rate(previous, current, sampledAt.Sub(previousAt))
				previous, previousAt = current, sampledAt
			} else if !errors.Is(sampleErr, mihomo.ErrNotRunning) {
				sample.ErrorCode = "sample_unavailable"
			}
			if err := writeOverviewSample(writer, sample); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) handleLiveOverviewStream(
	writer http.ResponseWriter,
	request *http.Request,
	flusher http.Flusher,
	liveTraffic TrafficLiveService,
) {
	subscription := liveTraffic.SubscribeLive()
	defer subscription.Close()
	live := liveTraffic.LiveSnapshot()
	history := make([]overview.Sample, 0, len(live.History))
	for _, sample := range live.History {
		history = append(history, overviewSampleFromLive(sample, s.overview.Status().Running))
	}
	if len(history) > 0 {
		if err := writeOverviewHistory(writer, history); err != nil {
			return
		}
		flusher.Flush()
	} else {
		previous, previousAt, _ := s.readOverviewSnapshot(request, time.Now().UTC())
		initial := overview.Sample{
			Timestamp: previousAt, ActiveConnections: len(previous.Connections), Running: s.overview.Status().Running,
		}
		if err := writeOverviewSample(writer, initial); err != nil {
			return
		}
		flusher.Flush()
	}
	for {
		select {
		case <-request.Context().Done():
			return
		case sample, ok := <-subscription.C:
			if !ok {
				return
			}
			if err := writeOverviewSample(writer, overviewSampleFromLive(sample, s.overview.Status().Running)); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func overviewSampleFromLive(sample metrics.LiveSample, running bool) overview.Sample {
	resources := make([]overview.ResourceSample, 0, len(sample.Resources))
	for _, resource := range sample.Resources {
		resources = append(resources, overview.ResourceSample{
			ResourceType:        resource.ResourceType,
			ResourceID:          resource.ResourceID,
			UploadBytesPerSec:   resource.UploadBytesPerSec,
			DownloadBytesPerSec: resource.DownloadBytesPerSec,
			ActiveConnections:   resource.ActiveConnections,
		})
	}
	return overview.Sample{
		Timestamp:           sample.Timestamp,
		UploadBytesPerSec:   sample.UploadBytesPerSec,
		DownloadBytesPerSec: sample.DownloadBytesPerSec,
		ActiveConnections:   int(sample.ActiveConnections),
		Running:             running,
		Resources:           resources,
	}
}

func writeOverviewHistory(writer http.ResponseWriter, samples []overview.Sample) error {
	payload, err := json.Marshal(samples)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "event: history\ndata: %s\n\n", payload)
	return err
}

func (s *Server) readOverviewSnapshot(request *http.Request, fallback time.Time) (overview.Snapshot, time.Time, error) {
	snapshot, err := s.overview.OverviewSnapshot(request.Context())
	return snapshot, fallback, err
}

func writeOverviewSample(writer http.ResponseWriter, sample overview.Sample) error {
	payload, err := json.Marshal(sample)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "event: sample\ndata: %s\n\n", payload)
	return err
}
