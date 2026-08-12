package email

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/notifications"
)

func TestSMTPClientSendsThroughRealTCPConnection(t *testing.T) {
	server := newSMTPServer(t)
	defer server.Close()
	host, port := server.Address(t)
	client, err := NewSMTP(Config{Host: host, Port: port, From: "work@example.test", FromName: "Work Management", TLSMode: TLSPlain, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Send(context.Background(), notifications.Message{To: "alice@example.test", ToName: "Alice", Subject: "New work assigned", TextBody: "Inspect the warehouse", MessageID: "<notification-1@example.test>"})
	if err != nil {
		t.Fatal(err)
	}
	message := server.Message(t)
	if !strings.Contains(message, "alice@example.test") || !strings.Contains(message, "Inspect the warehouse") {
		t.Fatalf("message=%s", message)
	}
}

type smtpServer struct {
	listener net.Listener
	mu       sync.Mutex
	message  string
	done     chan struct{}
}

func newSMTPServer(t *testing.T) *smtpServer {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &smtpServer{listener: listener, done: make(chan struct{})}
	go server.serve()
	return server
}
func (server *smtpServer) Address(t *testing.T) (string, int) {
	host, portText, _ := net.SplitHostPort(server.listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	return host, port
}
func (server *smtpServer) serve() {
	defer close(server.done)
	connection, err := server.listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = connection.Close() }()
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	write := func(format string, args ...any) { _, _ = fmt.Fprintf(writer, format, args...); _ = writer.Flush() }
	write("220 test SMTP\r\n")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(command, "EHLO"):
			write("250-test\r\n250 PIPELINING\r\n")
		case strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
			write("250 OK\r\n")
		case command == "DATA":
			write("354 End data\r\n")
			var lines []string
			for {
				dataLine, readErr := reader.ReadString('\n')
				if readErr != nil {
					return
				}
				if dataLine == ".\r\n" {
					break
				}
				lines = append(lines, dataLine)
			}
			server.mu.Lock()
			server.message = strings.Join(lines, "")
			server.mu.Unlock()
			write("250 accepted\r\n")
		case command == "QUIT":
			write("221 bye\r\n")
			return
		default:
			write("250 OK\r\n")
		}
	}
}
func (server *smtpServer) Message(t *testing.T) string {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		message := server.message
		server.mu.Unlock()
		if message != "" {
			return message
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no message")
	return ""
}
func (server *smtpServer) Close() {
	_ = server.listener.Close()
	select {
	case <-server.done:
	case <-time.After(time.Second):
	}
}
