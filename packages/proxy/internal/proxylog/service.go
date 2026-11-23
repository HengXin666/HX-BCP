package proxylog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrBusy = errors.New("real-time log stream limit reached")

type Event struct {
	Timestamp    time.Time `json:"timestamp"`
	Level        string    `json:"level"`
	Message      string    `json:"message"`
	ProxyGroupID string    `json:"proxy_group_id,omitempty"`
	ProxyGroup   string    `json:"proxy_group,omitempty"`
	NodeID       string    `json:"node_id,omitempty"`
	Node         string    `json:"node,omitempty"`
}

type Filter struct {
	Level      string
	ProxyGroup string
	Node       string
}

type Reader interface {
	Next(context.Context) (Event, error)
	Close() error
}

type Source interface {
	OpenLogStream(context.Context) (Reader, error)
}

type Service struct {
	source      Source
	streamSlots chan struct{}
	bufferSize  int
}

func NewService(source Source, maxStreams, bufferSize int) (*Service, error) {
	if source == nil {
		return nil, errors.New("proxy log source is required")
	}
	if maxStreams < 1 || maxStreams > 64 {
		return nil, errors.New("proxy log max streams must be between 1 and 64")
	}
	if bufferSize < 1 || bufferSize > 1024 {
		return nil, errors.New("proxy log buffer size must be between 1 and 1024")
	}
	return &Service{
		source:      source,
		streamSlots: make(chan struct{}, maxStreams),
		bufferSize:  bufferSize,
	}, nil
}

func NewDefaultService(source Source) (*Service, error) {
	return NewService(source, 8, 64)
}

func (s *Service) Subscribe(ctx context.Context, filter Filter) (<-chan Event, error) {
	filter.Level = strings.ToLower(strings.TrimSpace(filter.Level))
	filter.ProxyGroup = strings.TrimSpace(filter.ProxyGroup)
	filter.Node = strings.TrimSpace(filter.Node)
	if !validLevel(filter.Level) {
		return nil, fmt.Errorf("level must be debug, info, warning, or error")
	}
	select {
	case s.streamSlots <- struct{}{}:
	default:
		return nil, ErrBusy
	}

	reader, err := s.source.OpenLogStream(ctx)
	if err != nil {
		<-s.streamSlots
		return nil, err
	}
	events := make(chan Event, s.bufferSize)
	go func() {
		defer func() {
			_ = reader.Close()
			<-s.streamSlots
			close(events)
		}()
		for {
			event, readErr := reader.Next(ctx)
			if readErr != nil {
				return
			}
			if !matches(event, filter) {
				continue
			}
			select {
			case events <- event:
			default:
				// Real-time logs are transient. Keep the newest bounded window
				// when a browser cannot consume events quickly enough.
				select {
				case <-events:
				default:
				}
				select {
				case events <- event:
				default:
				}
			}
		}
	}()
	return events, nil
}

func validLevel(level string) bool {
	switch level {
	case "", "debug", "info", "warning", "error":
		return true
	default:
		return false
	}
}

func matches(event Event, filter Filter) bool {
	if filter.ProxyGroup != "" && event.ProxyGroup != filter.ProxyGroup {
		return false
	}
	if filter.Node != "" && event.Node != filter.Node && event.NodeID != filter.Node {
		return false
	}
	if filter.Level == "" {
		return true
	}
	return levelRank(event.Level) >= levelRank(filter.Level)
}

func levelRank(level string) int {
	switch strings.ToLower(level) {
	case "error":
		return 4
	case "warning", "warn":
		return 3
	case "info":
		return 2
	default:
		return 1
	}
}
