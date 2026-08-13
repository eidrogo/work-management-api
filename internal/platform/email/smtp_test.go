package email

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/textproto"
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

func TestSMTPRejectResponseIsClassifiedAndRedacted(t *testing.T) {
	server := newSMTPServer(t)
	server.rcptResponse = "550 provider-secret mailbox detail"
	defer server.Close()
	host, port := server.Address(t)
	client, err := NewSMTP(Config{Host: host, Port: port, From: "work@example.test", TLSMode: TLSPlain, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Send(context.Background(), notifications.Message{To: "alice@example.test", Subject: "New work assigned", TextBody: "Inspect", MessageID: "<notification-rejected@example.test>"})
	if err == nil {
		t.Fatal("recipient rejection unexpectedly succeeded")
	}
	if notifications.IsRetryable(err) {
		t.Fatal("550 recipient rejection should be permanent")
	}
	if got := notifications.FailureCodeOf(err); got != notifications.FailureSMTPRecipientRejected {
		t.Fatalf("failure code=%q", got)
	}
	if got := err.Error(); got != notifications.SafeFailureMessage(notifications.FailureSMTPRecipientRejected) || strings.Contains(got, "provider-secret") {
		t.Fatalf("unsafe error=%q", got)
	}
}

func TestSMTPFailureClassificationIsStageAwareAndSafe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		stage     smtpStage
		err       error
		code      notifications.FailureCode
		retryable bool
	}{
		{name: "421 temporary greeting", stage: smtpStageProtocol, err: &textproto.Error{Code: 421, Msg: "provider-secret greeting"}, code: notifications.FailureSMTPTemporary, retryable: true},
		{name: "450 temporary recipient", stage: smtpStageRecipient, err: &textproto.Error{Code: 450, Msg: "provider-secret recipient"}, code: notifications.FailureSMTPTemporary, retryable: true},
		{name: "535 authentication", stage: smtpStageAuthentication, err: &textproto.Error{Code: 535, Msg: "provider-secret auth"}, code: notifications.FailureSMTPAuthentication, retryable: false},
		{name: "554 unclassified protocol", stage: smtpStageProtocol, err: &textproto.Error{Code: 554, Msg: "provider-secret protocol"}, code: notifications.FailureSMTPProtocol, retryable: true},
		{name: "timeout", stage: smtpStageConnection, err: context.DeadlineExceeded, code: notifications.FailureSMTPTimeout, retryable: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := classify(test.stage, test.err)
			if got := notifications.FailureCodeOf(err); got != test.code {
				t.Fatalf("code=%q", got)
			}
			if got := notifications.IsRetryable(err); got != test.retryable {
				t.Fatalf("retryable=%v", got)
			}
			if strings.Contains(err.Error(), "provider-secret") {
				t.Fatalf("unsafe error=%q", err.Error())
			}
		})
	}
}

func TestSMTPMissingSTARTTLSIsPermanentConfigurationFailure(t *testing.T) {
	server := newSMTPServer(t)
	defer server.Close()
	host, port := server.Address(t)
	client, err := NewSMTP(Config{Host: host, Port: port, From: "work@example.test", TLSMode: TLSSTARTTLS, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Send(context.Background(), notifications.Message{To: "alice@example.test", Subject: "New work assigned", TextBody: "Inspect", MessageID: "<notification-starttls@example.test>"})
	if err == nil || notifications.IsRetryable(err) || notifications.FailureCodeOf(err) != notifications.FailureSMTPConfiguration {
		t.Fatalf("error=%v code=%q retryable=%v", err, notifications.FailureCodeOf(err), notifications.IsRetryable(err))
	}
}

type smtpServer struct {
	listener     net.Listener
	mu           sync.Mutex
	message      string
	rcptResponse string
	done         chan struct{}
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
		case strings.HasPrefix(command, "MAIL FROM"):
			write("250 OK\r\n")
		case strings.HasPrefix(command, "RCPT TO"):
			if server.rcptResponse != "" {
				write("%s\r\n", server.rcptResponse)
				return
			}
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
