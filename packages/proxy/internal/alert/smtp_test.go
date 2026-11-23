package alert

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// miniSMTPServer implements just enough of RFC 5321 to accept one plain
// (security "none", no AUTH) delivery and record the submitted message.
type miniSMTPServer struct {
	listener net.Listener
	mutex    sync.Mutex
	from     string
	rcpts    []string
	data     string
}

func startMiniSMTPServer(t *testing.T) *miniSMTPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &miniSMTPServer{listener: listener}
	go server.serveOne(t)
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func (s *miniSMTPServer) serveOne(t *testing.T) {
	connection, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(connection)
	write := func(line string) { _, _ = connection.Write([]byte(line + "\r\n")) }

	write("220 mini ESMTP ready")
	inData := false
	var body strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				s.mutex.Lock()
				s.data = body.String()
				s.mutex.Unlock()
				inData = false
				write("250 accepted")
				continue
			}
			body.WriteString(line + "\r\n")
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			write("250-mini")
			write("250 SIZE 1048576")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			s.mutex.Lock()
			s.from = line[len("MAIL FROM:"):]
			s.mutex.Unlock()
			write("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			s.mutex.Lock()
			s.rcpts = append(s.rcpts, line[len("RCPT TO:"):])
			s.mutex.Unlock()
			write("250 ok")
		case upper == "DATA":
			inData = true
			write("354 go ahead")
		case upper == "QUIT":
			write("221 bye")
			return
		default:
			write("250 ok")
		}
	}
}

func (s *miniSMTPServer) message() (string, []string, string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.from, append([]string(nil), s.rcpts...), s.data
}

func TestSMTPChannelDeliversPlainMail(t *testing.T) {
	server := startMiniSMTPServer(t)
	address := server.listener.Addr().(*net.TCPAddr)

	channel := NewSMTPChannel(SMTPConfig{
		Host:     "127.0.0.1",
		Port:     address.Port,
		Security: "none",
		From:     "alerts@example.com",
		To:       []string{"admin@example.com", "ops@example.com"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := channel.Send(ctx, "[HX-Pxy] FIRING warning: subscription-refresh-failing", "Message: refresh failed\r\n"); err != nil {
		t.Fatalf("send: %v", err)
	}
	from, rcpts, data := server.message()
	if !strings.Contains(from, "alerts@example.com") {
		t.Fatalf("unexpected MAIL FROM %q", from)
	}
	if len(rcpts) != 2 {
		t.Fatalf("expected 2 recipients, got %v", rcpts)
	}
	if !strings.Contains(data, "Subject: [HX-Pxy] FIRING warning: subscription-refresh-failing") {
		t.Fatalf("subject missing in message:\n%s", data)
	}
	if !strings.Contains(data, "refresh failed") {
		t.Fatalf("body missing in message:\n%s", data)
	}
}

func TestSMTPChannelStartTLSRequiredButMissing(t *testing.T) {
	server := startMiniSMTPServer(t)
	address := server.listener.Addr().(*net.TCPAddr)
	channel := NewSMTPChannel(SMTPConfig{
		Host:     "127.0.0.1",
		Port:     address.Port,
		Security: "starttls",
		From:     "alerts@example.com",
		To:       []string{"admin@example.com"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := channel.Send(ctx, "subject", "body"); err == nil {
		t.Fatal("expected error when the server lacks STARTTLS")
	}
}
