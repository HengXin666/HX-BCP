package proxylog

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

func TestSubscribeFiltersGroupNodeAndLevel(t *testing.T) {
	t.Parallel()
	source := &fakeSource{events: []Event{
		{Level: "debug", ProxyGroup: "fast", Node: "jp"},
		{Level: "warning", ProxyGroup: "fast", Node: "us"},
		{Level: "error", ProxyGroup: "slow", Node: "us"},
	}}
	service, err := NewService(source, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	events, err := service.Subscribe(context.Background(), Filter{Level: "info", ProxyGroup: "fast", Node: "us"})
	if err != nil {
		t.Fatal(err)
	}
	event, ok := <-events
	if !ok || event.Level != "warning" || event.Node != "us" {
		t.Fatalf("event = %#v, open = %v", event, ok)
	}
	if _, ok := <-events; ok {
		t.Fatal("unexpected second filtered event")
	}
}

func TestSubscribeEnforcesStreamLimitAndReleasesSlot(t *testing.T) {
	t.Parallel()
	reader := &blockingReader{closed: make(chan struct{})}
	service, err := NewService(&fakeSource{reader: reader}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	first, err := service.Subscribe(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Subscribe(context.Background(), Filter{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Subscribe error = %v, want ErrBusy", err)
	}
	cancel()
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("subscription did not stop after cancellation")
	}

	deadline := time.Now().Add(time.Second)
	for {
		thirdCtx, thirdCancel := context.WithCancel(context.Background())
		third, subscribeErr := service.Subscribe(thirdCtx, Filter{})
		if subscribeErr == nil {
			thirdCancel()
			<-third
			break
		}
		thirdCancel()
		if !errors.Is(subscribeErr, ErrBusy) || time.Now().After(deadline) {
			t.Fatalf("third Subscribe error = %v", subscribeErr)
		}
		time.Sleep(time.Millisecond)
	}
}

type fakeSource struct {
	events []Event
	reader Reader
}

func (s *fakeSource) OpenLogStream(context.Context) (Reader, error) {
	if s.reader != nil {
		return s.reader, nil
	}
	return &sliceReader{events: append([]Event(nil), s.events...)}, nil
}

type sliceReader struct {
	events []Event
}

func (r *sliceReader) Next(context.Context) (Event, error) {
	if len(r.events) == 0 {
		return Event{}, io.EOF
	}
	event := r.events[0]
	r.events = r.events[1:]
	return event, nil
}

func (*sliceReader) Close() error { return nil }

type blockingReader struct {
	closed chan struct{}
	once   sync.Once
}

func (r *blockingReader) Next(ctx context.Context) (Event, error) {
	select {
	case <-ctx.Done():
		return Event{}, ctx.Err()
	case <-r.closed:
		return Event{}, io.EOF
	}
}

func (r *blockingReader) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}
