package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// requestRemoteExec dials the privileged helper, sends one frameExec and waits
// for the frameExecResult (or frameError) reply.
func requestRemoteExec(ctx context.Context, socketPath, command string, timeout time.Duration) (ExecResult, error) {
	requestContext, cancel := context.WithTimeout(ctx, timeout+10*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(requestContext, "unix", socketPath)
	if err != nil {
		return ExecResult{}, fmt.Errorf("connect privileged exec helper: %w", err)
	}
	defer connection.Close()
	stop := context.AfterFunc(requestContext, func() { _ = connection.Close() })
	defer stop()
	_ = connection.SetDeadline(time.Now().Add(timeout + 10*time.Second))
	payload, err := json.Marshal(execRequest{
		Command:        command,
		TimeoutSeconds: int(timeout.Seconds()),
	})
	if err != nil {
		return ExecResult{}, err
	}
	if err := writeFrame(connection, frameExec, payload); err != nil {
		return ExecResult{}, fmt.Errorf("request remote exec: %w", err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(timeout + 10*time.Second))
	kind, reply, err := readFrame(connection)
	if err != nil {
		return ExecResult{}, fmt.Errorf("read remote exec response: %w", err)
	}
	switch kind {
	case frameExecResult:
		var result ExecResult
		if err := json.Unmarshal(reply, &result); err != nil {
			return ExecResult{}, fmt.Errorf("decode remote exec result: %w", err)
		}
		return result, nil
	case frameError:
		return ExecResult{}, fmt.Errorf("remote exec failed: %s", string(reply))
	default:
		return ExecResult{}, fmt.Errorf("unexpected remote exec response frame %d", kind)
	}
}
