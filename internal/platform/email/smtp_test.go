package email

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
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

func TestSMTPClientNegotiatesSTARTTLS(t *testing.T) {
	server := newTLSSMTPServer(t, true, false, true)
	defer server.Close()
	host, port := server.Address(t)
	client, err := NewSMTP(Config{
		Host: host, Port: port, From: "work@example.test", FromName: "Work Management",
		Username: "smtp-user", Password: "smtp-password", TLSMode: TLSSTARTTLS,
		TLSServerName: "localhost", TLSInsecureSkipVerify: true, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	message := notifications.Message{To: "alice@example.test", ToName: "Alice", Subject: "New work assigned", TextBody: "Inspect the warehouse", MessageID: "<notification-starttls@example.test>"}
	if err = client.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	got, encrypted, authenticated, ehloCount := server.Result(t)
	if !encrypted || !authenticated || ehloCount < 2 {
		t.Fatalf("encrypted=%v authenticated=%v ehlo=%d", encrypted, authenticated, ehloCount)
	}
	server.mu.Lock()
	username, password := server.authUsername, server.authPassword
	server.mu.Unlock()
	if username != "smtp-user" || password != "smtp-password" {
		t.Fatalf("AUTH username=%q password=%q", username, password)
	}
	assertSMTPMessage(t, got, message)
}

func TestSMTPClientNegotiatesImplicitTLS(t *testing.T) {
	server := newTLSSMTPServer(t, false, true, false)
	defer server.Close()
	host, port := server.Address(t)
	client, err := NewSMTP(Config{Host: host, Port: port, From: "work@example.test", TLSMode: TLSImplicit, TLSServerName: "localhost", TLSInsecureSkipVerify: true, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	message := notifications.Message{To: "alice@example.test", Subject: "Implicit TLS", TextBody: "Encrypted from connect", MessageID: "<notification-tls@example.test>"}
	if err = client.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	got, encrypted, _, _ := server.Result(t)
	if !encrypted {
		t.Fatal("implicit SMTP transaction was not encrypted")
	}
	assertSMTPMessage(t, got, message)
}

func TestSMTPClientRejectsUntrustedCertificate(t *testing.T) {
	for _, mode := range []TLSMode{TLSSTARTTLS, TLSImplicit} {
		t.Run(string(mode), func(t *testing.T) {
			server := newTLSSMTPServer(t, mode == TLSSTARTTLS, mode == TLSImplicit, false)
			defer server.Close()
			host, port := server.Address(t)
			client, err := NewSMTP(Config{Host: host, Port: port, From: "work@example.test", TLSMode: mode, TLSServerName: "localhost", Timeout: 2 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			err = client.Send(context.Background(), notifications.Message{To: "alice@example.test", Subject: "Reject cert", TextBody: "body", MessageID: "<notification-cert@example.test>"})
			if err == nil || !notifications.IsRetryable(err) || notifications.FailureCodeOf(err) != notifications.FailureSMTPTLS {
				t.Fatalf("error=%v code=%q retryable=%v", err, notifications.FailureCodeOf(err), notifications.IsRetryable(err))
			}
			if time.Since(started) > time.Second {
				t.Fatal("certificate rejection exceeded one second")
			}
			if strings.Contains(err.Error(), "x509") || strings.Contains(err.Error(), "certificate") || strings.Contains(err.Error(), "localhost") {
				t.Fatalf("unsafe TLS error=%q", err.Error())
			}
		})
	}
}

func TestSMTPClientClassifiesRealAUTHFailure(t *testing.T) {
	server := newTLSSMTPServer(t, true, false, true)
	server.authResponse = "535 provider-secret authentication detail"
	defer server.Close()
	host, port := server.Address(t)
	client, err := NewSMTP(Config{Host: host, Port: port, From: "work@example.test", Username: "smtp-user", Password: "smtp-password", TLSMode: TLSSTARTTLS, TLSServerName: "localhost", TLSInsecureSkipVerify: true, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Send(context.Background(), notifications.Message{To: "alice@example.test", Subject: "AUTH failure", TextBody: "body", MessageID: "<notification-auth-failure@example.test>"})
	if err == nil || notifications.IsRetryable(err) || notifications.FailureCodeOf(err) != notifications.FailureSMTPAuthentication {
		t.Fatalf("error=%v code=%q retryable=%v", err, notifications.FailureCodeOf(err), notifications.IsRetryable(err))
	}
	if strings.Contains(err.Error(), "provider-secret") || strings.Contains(err.Error(), "smtp-password") {
		t.Fatalf("unsafe AUTH error=%q", err.Error())
	}
}

func TestSMTPClientTimeoutIsBoundedAndRetryable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			defer func() { _ = connection.Close() }()
			_, _ = io.Copy(io.Discard, connection)
		}
	}()
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	client, err := NewSMTP(Config{Host: host, Port: port, From: "work@example.test", TLSMode: TLSPlain, Timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = client.Send(context.Background(), notifications.Message{To: "alice@example.test", Subject: "Timeout", TextBody: "body", MessageID: "<notification-timeout@example.test>"})
	elapsed := time.Since(started)
	_ = listener.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout test server did not stop")
	}
	if err == nil || !notifications.IsRetryable(err) || notifications.FailureCodeOf(err) != notifications.FailureSMTPTimeout {
		t.Fatalf("error=%v code=%q retryable=%v", err, notifications.FailureCodeOf(err), notifications.IsRetryable(err))
	}
	if elapsed < 75*time.Millisecond || elapsed > time.Second {
		t.Fatalf("elapsed=%s", elapsed)
	}
	if strings.Contains(err.Error(), host) || strings.Contains(err.Error(), "read tcp") {
		t.Fatalf("unsafe timeout error=%q", err.Error())
	}
}

func TestSMTPClientRejectsHeaderInjectionBeforeDial(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	client, err := NewSMTP(Config{Host: host, Port: port, From: "work@example.test", TLSMode: TLSPlain, Timeout: 250 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []notifications.Message{
		{To: "alice@example.test", ToName: "Alice\r\nBcc: attacker@example.test", Subject: "Subject", MessageID: "<id@example.test>"},
		{To: "alice@example.test", Subject: "Subject\r\nBcc: attacker@example.test", MessageID: "<id@example.test>"},
		{To: "alice@example.test", Subject: "Subject", MessageID: "<id@example.test>\r\nBcc: attacker@example.test"},
	} {
		err = client.Send(context.Background(), message)
		if err == nil || notifications.IsRetryable(err) || notifications.FailureCodeOf(err) != notifications.FailureSMTPConfiguration {
			t.Fatalf("message=%#v error=%v", message, err)
		}
	}
	if tcp, ok := listener.(*net.TCPListener); ok {
		_ = tcp.SetDeadline(time.Now().Add(50 * time.Millisecond))
	}
	connection, acceptErr := listener.Accept()
	if acceptErr == nil {
		_ = connection.Close()
		t.Fatal("SMTP server observed a connection for rejected headers")
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

type tlsSMTPServer struct {
	listener                 net.Listener
	config                   *tls.Config
	startTLS                 bool
	implicit                 bool
	auth                     bool
	authResponse             string
	authUsername             string
	authPassword             string
	done                     chan struct{}
	mu                       sync.Mutex
	message                  string
	encrypted, authenticated bool
	ehloCount                int
}

func newTLSSMTPServer(t *testing.T, startTLS, implicit, auth bool) *tlsSMTPServer {
	t.Helper()
	certificate := testCertificate(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &tlsSMTPServer{
		listener: listener,
		config:   &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}},
		startTLS: startTLS,
		implicit: implicit,
		auth:     auth,
		done:     make(chan struct{}),
	}
	go server.serve()
	return server
}

func (server *tlsSMTPServer) Address(t *testing.T) (string, int) {
	t.Helper()
	host, portText, err := net.SplitHostPort(server.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

func (server *tlsSMTPServer) serve() {
	defer close(server.done)
	connection, err := server.listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = connection.Close() }()
	encrypted := false
	if server.implicit {
		tlsConnection := tls.Server(connection, server.config)
		if err = tlsConnection.Handshake(); err != nil {
			return
		}
		connection = tlsConnection
		encrypted = true
	}
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	write := func(format string, args ...any) bool {
		if _, writeErr := fmt.Fprintf(writer, format, args...); writeErr != nil {
			return false
		}
		return writer.Flush() == nil
	}
	if !write("220 test SMTP\r\n") {
		return
	}
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return
		}
		command := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(command, "EHLO"):
			server.mu.Lock()
			server.ehloCount++
			server.mu.Unlock()
			if server.startTLS && !encrypted {
				if !write("250-test\r\n250 STARTTLS\r\n") {
					return
				}
			} else if server.auth {
				if !write("250-test\r\n250 AUTH PLAIN\r\n") {
					return
				}
			} else if !write("250 test\r\n") {
				return
			}
		case command == "STARTTLS" && server.startTLS && !encrypted:
			if !write("220 ready for TLS\r\n") {
				return
			}
			tlsConnection := tls.Server(connection, server.config)
			if err = tlsConnection.Handshake(); err != nil {
				return
			}
			connection = tlsConnection
			reader = bufio.NewReader(connection)
			writer = bufio.NewWriter(connection)
			encrypted = true
			server.mu.Lock()
			server.encrypted = true
			server.mu.Unlock()
		case strings.HasPrefix(command, "AUTH PLAIN") && server.auth && encrypted:
			parts := strings.Fields(command)
			if len(parts) == 3 {
				if decoded, decodeErr := base64.StdEncoding.DecodeString(parts[2]); decodeErr == nil {
					credentials := strings.Split(string(decoded), "\x00")
					if len(credentials) == 3 {
						server.authUsername, server.authPassword = credentials[1], credentials[2]
					}
				}
			}
			server.mu.Lock()
			server.authenticated = true
			server.mu.Unlock()
			response := server.authResponse
			if response == "" {
				response = "235 authenticated"
			}
			if !write("%s\r\n", response) {
				return
			}
		case strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
			if !write("250 OK\r\n") {
				return
			}
		case command == "DATA":
			if !write("354 End data\r\n") {
				return
			}
			var lines []string
			for {
				dataLine, dataErr := reader.ReadString('\n')
				if dataErr != nil {
					return
				}
				if dataLine == ".\r\n" {
					break
				}
				lines = append(lines, dataLine)
			}
			server.mu.Lock()
			server.message = strings.Join(lines, "")
			server.encrypted = encrypted
			server.mu.Unlock()
			if !write("250 accepted\r\n") {
				return
			}
		case command == "QUIT":
			_ = write("221 bye\r\n")
			return
		default:
			if !write("500 unsupported\r\n") {
				return
			}
		}
	}
}

func (server *tlsSMTPServer) Result(t *testing.T) (string, bool, bool, int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		message, encrypted, authenticated, ehloCount := server.message, server.encrypted, server.authenticated, server.ehloCount
		server.mu.Unlock()
		if message != "" {
			return message, encrypted, authenticated, ehloCount
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no TLS SMTP message")
	return "", false, false, 0
}

func (server *tlsSMTPServer) Close() {
	_ = server.listener.Close()
	select {
	case <-server.done:
	case <-time.After(time.Second):
	}
}

func assertSMTPMessage(t *testing.T, got string, want notifications.Message) {
	t.Helper()
	for _, fragment := range []string{want.To, want.Subject, want.TextBody, "Message-ID: " + want.MessageID} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("message omits %q:\n%s", fragment, got)
		}
	}
}

func testCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}
