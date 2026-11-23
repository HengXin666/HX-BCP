package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

// maxExecCommandLength bounds a single remote command; maxExecOutputBytes
// truncates captured output so a runaway command cannot exhaust memory.
const (
	maxExecCommandLength = 16 * 1024
	maxExecOutputBytes   = 1 << 20 // 1 MiB per stream
	defaultExecTimeout   = 60 * time.Second
	maxExecTimeout       = 300 * time.Second
)

// execRequest is the frameExec payload sent by the control plane.
type execRequest struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// execResult is the frameExecResult payload returned to the control plane.
type execResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// handleHelperExecRequest executes one non-interactive command on the helper
// (root) side and replies with a single frameExecResult. This is the API-drive
// remote-management path (20260823 user request): authenticated operators run
// bounded commands without a full PTY session.
func handleHelperExecRequest(connection net.Conn, payload []byte) {
	replyError := func(message string) {
		_ = writeFrame(connection, frameError, []byte(message))
	}
	var request execRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		replyError("invalid exec request: " + err.Error())
		return
	}
	command := strings.TrimSpace(request.Command)
	if command == "" {
		replyError("exec command is required")
		return
	}
	if len(command) > maxExecCommandLength {
		replyError(fmt.Sprintf("exec command exceeds %d bytes", maxExecCommandLength))
		return
	}
	timeout := defaultExecTimeout
	if request.TimeoutSeconds > 0 {
		timeout = time.Duration(request.TimeoutSeconds) * time.Second
	}
	if timeout > maxExecTimeout {
		timeout = maxExecTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	commandContext := exec.CommandContext(ctx, "sh", "-c", command)
	var stdout, stderr limitedBuffer
	commandContext.Stdout = &stdout
	commandContext.Stderr = &stderr
	runErr := commandContext.Run()
	exitCode := 0
	if runErr != nil {
		if exitError, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1 // failed to start or killed by timeout
		}
	}
	result := execResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}
	payload, err := json.Marshal(result)
	if err != nil {
		replyError("encode exec result: " + err.Error())
		return
	}
	_ = writeFrame(connection, frameExecResult, payload)
}

// limitedBuffer captures output up to maxExecOutputBytes.
type limitedBuffer struct {
	buffer []byte
	trunc  bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := maxExecOutputBytes - len(b.buffer)
	if remaining <= 0 {
		b.trunc = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.buffer = append(b.buffer, p[:remaining]...)
		b.trunc = true
		return len(p), nil
	}
	b.buffer = append(b.buffer, p...)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	if b.trunc {
		return string(b.buffer) + "\n...[输出截断]"
	}
	return string(b.buffer)
}
