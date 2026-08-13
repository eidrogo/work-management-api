package email

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eidrogo/work-management-api/internal/notifications"
)

func TestSMTPClientRejectsOversizedServerResponsesWithinReadBudget(t *testing.T) {
	tests := []struct {
		name, stage string
		mode        TLSMode
		multiline   bool
		auth        bool
	}{
		{name: "plain greeting single line", stage: "greeting", mode: TLSPlain},
		{name: "plain EHLO multiline", stage: "ehlo", mode: TLSPlain, multiline: true},
		{name: "STARTTLS pre-upgrade EHLO multiline", stage: "ehlo", mode: TLSSTARTTLS, multiline: true},
		{name: "STARTTLS command single line", stage: "starttls", mode: TLSSTARTTLS},
		{name: "STARTTLS encrypted EHLO multiline", stage: "encrypted-ehlo", mode: TLSSTARTTLS, multiline: true},
		{name: "STARTTLS cumulative pre-and-post-upgrade responses", stage: "cumulative-starttls", mode: TLSSTARTTLS},
		{name: "implicit TLS greeting multiline", stage: "greeting", mode: TLSImplicit, multiline: true},
		{name: "AUTH single line", stage: "auth", mode: TLSSTARTTLS, auth: true},
		{name: "MAIL multiline", stage: "mail", mode: TLSPlain, multiline: true},
		{name: "RCPT single line", stage: "rcpt", mode: TLSPlain},
		{name: "DATA command multiline", stage: "data", mode: TLSPlain, multiline: true},
		{name: "post DATA single line", stage: "post-data", mode: TLSPlain},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newOversizedResponseSMTPServer(t, test.mode, test.stage, test.multiline, test.auth)
			defer server.Close()
			host, port := server.Address(t)
			config := Config{Host: host, Port: port, From: "work@example.test", TLSMode: test.mode, TLSServerName: "localhost", TLSInsecureSkipVerify: true, Timeout: 2 * time.Second}
			if test.auth {
				config.Username, config.Password = "smtp-user", "smtp-password"
			}
			client, err := NewSMTP(config)
			if err != nil {
				t.Fatal(err)
			}
			err = client.Send(context.Background(), notifications.Message{To: "alice@example.test", Subject: "Oversized response", TextBody: "body", MessageID: "<oversized@example.test>"})
			if err == nil {
				t.Fatal("oversized SMTP response unexpectedly succeeded")
			}
			if notifications.IsRetryable(err) || notifications.FailureCodeOf(err) != notifications.FailureSMTPProtocol {
				t.Fatalf("error=%v code=%q retryable=%v", err, notifications.FailureCodeOf(err), notifications.IsRetryable(err))
			}
			maximumServerWrite := maximumSMTPResponseBytes + maximumSMTPResponseWriteSlack
			if test.stage == "cumulative-starttls" {
				maximumServerWrite = 2*maximumSMTPResponseBytes*3/5 + maximumSMTPResponseWriteSlack
			}
			if server.BytesWritten() > maximumServerWrite {
				t.Fatalf("server wrote %d bytes before client closed, maximum=%d", server.BytesWritten(), maximumServerWrite)
			}
		})
	}
}

func TestSMTPClientDoesNotReadAfterAcceptedDATA(t *testing.T) {
	server := newOversizedResponseSMTPServer(t, TLSPlain, "quit", false, false)
	defer server.Close()
	host, port := server.Address(t)
	client, err := NewSMTP(Config{Host: host, Port: port, From: "work@example.test", TLSMode: TLSPlain, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Send(context.Background(), notifications.Message{To: "alice@example.test", Subject: "Accepted", TextBody: "body", MessageID: "<accepted@example.test>"}); err != nil {
		t.Fatal(err)
	}
	if server.BytesWritten() > 128 {
		t.Fatalf("client issued QUIT and read an unnecessary server response after DATA acceptance: server bytes=%d", server.BytesWritten())
	}
}

func TestSMTPResponseReadBudgetAcceptsExactBoundaryAndRejectsNextByte(t *testing.T) {
	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
	limited := newResponseLimitedConn(left)
	payload := bytes.Repeat([]byte{'x'}, maximumSMTPResponseBytes+1)
	writeResult := make(chan error, 1)
	go func() {
		_, err := right.Write(payload)
		writeResult <- err
	}()
	accepted := make([]byte, maximumSMTPResponseBytes)
	if _, err := io.ReadFull(limited, accepted); err != nil {
		t.Fatalf("exact response budget rejected: %v", err)
	}
	var next [1]byte
	if _, err := limited.Read(next[:]); !errors.Is(err, errSMTPResponseTooLarge) {
		t.Fatalf("budget+1 read error=%v", err)
	}
	if !errors.Is(limited.Err(), errSMTPResponseTooLarge) {
		t.Fatalf("sticky limit error=%v", limited.Err())
	}
	_ = left.Close()
	select {
	case <-writeResult:
	case <-time.After(time.Second):
		t.Fatal("writer did not unblock after bounded connection close")
	}
}

func TestWriteSMTPPayloadDoesNotCloseAfterWriteFailure(t *testing.T) {
	writeFailure := errors.New("DATA payload write failed")
	writer := &failingSMTPDataWriter{writeErr: writeFailure}
	payloadCopied, err := writeSMTPPayload(writer, "payload")
	if !errors.Is(err, writeFailure) || payloadCopied {
		t.Fatalf("payloadCopied=%v error=%v", payloadCopied, err)
	}
	if writer.closeCalls != 0 {
		t.Fatalf("Close called %d time(s) after a failed payload write; Close would read a server response", writer.closeCalls)
	}
}

type failingSMTPDataWriter struct {
	writeErr   error
	closeCalls int
}

func (writer *failingSMTPDataWriter) Write([]byte) (int, error) {
	return 0, writer.writeErr
}

func (writer *failingSMTPDataWriter) Close() error {
	writer.closeCalls++
	return errSMTPResponseTooLarge
}

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
	server := newTLSSMTPServer(t, false, true, true)
	defer server.Close()
	host, port := server.Address(t)
	client, err := NewSMTP(Config{
		Host: host, Port: port, From: "work@example.test",
		Username: "smtp-user", Password: "smtp-password", TLSMode: TLSImplicit,
		TLSServerName: "localhost", TLSInsecureSkipVerify: true, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	message := notifications.Message{To: "alice@example.test", Subject: "Implicit TLS", TextBody: "Encrypted from connect", MessageID: "<notification-tls@example.test>"}
	if err = client.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	got, encrypted, authenticated, _ := server.Result(t)
	if !encrypted || !authenticated {
		t.Fatalf("encrypted=%v authenticated=%v", encrypted, authenticated)
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

func TestSMTPClientCancellationInterruptsBlockedTransaction(t *testing.T) {
	server := newSMTPServer(t)
	server.dataAccepted = make(chan struct{})
	server.releaseDataResponse = make(chan struct{})
	defer server.Close()
	host, port := server.Address(t)
	client, err := NewSMTP(Config{Host: host, Port: port, From: "work@example.test", TLSMode: TLSPlain, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- client.Send(ctx, notifications.Message{To: "alice@example.test", Subject: "Cancel blocked SMTP", TextBody: "body", MessageID: "<notification-cancel@example.test>"})
	}()
	select {
	case <-server.dataAccepted:
	case <-time.After(time.Second):
		t.Fatal("SMTP transaction did not reach post-DATA response")
	}
	started := time.Now()
	cancel()
	select {
	case err = <-result:
		if err == nil || !notifications.IsRetryable(err) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("context cancellation did not interrupt the SMTP transaction")
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("cancellation elapsed=%s", elapsed)
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
		{To: "alice@example.test", ToName: strings.Repeat("a", 201), Subject: "Subject", MessageID: "<id@example.test>"},
		{To: "alice@example.test", ToName: "Alice\tAdmin", Subject: "Subject", MessageID: "<id@example.test>"},
		{To: "alice@example.test", ToName: "Alice\u0085Admin", Subject: "Subject", MessageID: "<id@example.test>"},
		{To: "alice@example.test", ToName: string([]byte{'A', 0xff}), Subject: "Subject", MessageID: "<id@example.test>"},
		{To: "=?utf-8?q?bad=00name?= <alice@example.test>", Subject: "Subject", MessageID: "<id@example.test>"},
		{To: "alice@example.test", Subject: "Subject\r\nBcc: attacker@example.test", MessageID: "<id@example.test>"},
		{To: "alice@example.test", Subject: "Subject", MessageID: "<id@example.test>\r\nBcc: attacker@example.test"},
		{To: "alice@example.test", Subject: "Subject", MessageID: "<" + strings.Repeat("x", 1000) + "@example.test>"},
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

func TestBuildMessageBoundsAcceptedReasonAndRecipientNameLines(t *testing.T) {
	tests := []struct {
		name     string
		toName   string
		textBody string
	}{
		{name: "1000 ASCII reason", toName: "Alice", textBody: "Assignment reason: " + strings.Repeat("a", 1000) + "\n"},
		{name: "200 emoji recipient name", toName: strings.Repeat("😀", 200), textBody: "body\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			from := &mail.Address{Name: "Work Management", Address: "work@example.test"}
			to := &mail.Address{Name: test.toName, Address: "alice@example.test"}
			payload, err := buildMessage(from, to, notifications.Message{
				To: to.Address, ToName: to.Name, Subject: "New work assigned", TextBody: test.textBody,
				MessageID: "<notification-boundary@example.test>",
			})
			if err != nil {
				t.Fatal(err)
			}
			if maximum := maximumPhysicalLineLength(payload); maximum > 998 {
				t.Fatalf("maximum serialized line=%d octets, want <=998", maximum)
			}
		})
	}
}

func TestBuildMessagePreservesAcceptedBoundaryMIMEValues(t *testing.T) {
	longAddress := strings.Repeat("l", 64) + "@" + strings.Repeat("d", 63) + "." + strings.Repeat("e", 63) + "." + strings.Repeat("f", 61)
	boundarySubject := "New work assigned: " + strings.Repeat("😀", 60) + "…"
	tests := []struct {
		name, fromName, fromAddress, toName, toAddress, subject, textBody string
	}{
		{name: "1000 ASCII reason", fromName: "Work Management", fromAddress: "work@example.test", toName: "Alice", toAddress: "alice@example.test", subject: "New work assigned: Demo item", textBody: "Assignment reason: " + strings.Repeat("a", 1000) + "\n"},
		{name: "1000 four-byte reason and 200 ASCII names", fromName: strings.Repeat("S", 200), fromAddress: "work@example.test", toName: strings.Repeat("R", 200), toAddress: "alice@example.test", subject: boundarySubject, textBody: "Assignment reason: " + strings.Repeat("😀", 1000) + "\n"},
		{name: "200 two-byte names and long addresses", fromName: strings.Repeat("é", 200), fromAddress: longAddress, toName: strings.Repeat("é", 200), toAddress: longAddress, subject: boundarySubject, textBody: "complete reason\n"},
		{name: "200 four-byte names", fromName: strings.Repeat("😀", 200), fromAddress: "work@example.test", toName: strings.Repeat("😀", 200), toAddress: "alice@example.test", subject: boundarySubject, textBody: "complete reason\n"},
		{name: "quoted local part remains indivisible", fromName: "Sender", fromAddress: "sender@example.test", toName: "Alice  Admin", toAddress: "alice admin@example.test", subject: "New work assigned", textBody: "complete reason\n"},
		{name: "accepted subject controls are transfer encoded", fromName: "Sender", fromAddress: "sender@example.test", toName: "Alice", toAddress: "alice@example.test", subject: "New work assigned: Control\tTitle\u0085", textBody: "complete reason\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			from := &mail.Address{Name: test.fromName, Address: test.fromAddress}
			to := &mail.Address{Name: test.toName, Address: test.toAddress}
			message := notifications.Message{To: test.toAddress, ToName: test.toName, Subject: test.subject, TextBody: test.textBody, MessageID: "<notification-mime-boundary@example.test>"}
			payload, err := buildMessage(from, to, message)
			if err != nil {
				t.Fatal(err)
			}
			assertMIMEMessage(t, payload, from, to, message)
			if test.name == "1000 ASCII reason" && !strings.Contains(payload, "\r\nSubject: New work assigned: Demo item\r\n") {
				t.Fatalf("short ASCII demo Subject changed:\n%s", payload)
			}
		})
	}
}

func TestSMTPClientLineEnforcingServerAcceptsMaximumASCIIReason(t *testing.T) {
	server := newSMTPServer(t)
	server.maximumDataLineOctets = 998
	defer server.Close()
	host, port := server.Address(t)
	client, err := NewSMTP(Config{Host: host, Port: port, From: "work@example.test", FromName: "Work Management", TLSMode: TLSPlain, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	message := notifications.Message{
		To: "alice@example.test", ToName: "Alice", Subject: "New work assigned: Boundary item",
		TextBody: "Assignment reason: " + strings.Repeat("a", 1000) + "\n", MessageID: "<notification-line-boundary@example.test>",
	}
	err = client.Send(context.Background(), message)
	payload := server.Message(t)
	if err != nil {
		t.Fatalf("send error=%v maximum DATA line=%d", err, server.MaximumObservedLine())
	}
	assertMIMEMessage(t, payload, client.from, &mail.Address{Name: message.ToName, Address: message.To}, message)
}

func TestSMTPClientLineEnforcingServerAcceptsWorstCaseUnicodeBoundaries(t *testing.T) {
	server := newSMTPServer(t)
	server.maximumDataLineOctets = 998
	defer server.Close()
	host, port := server.Address(t)
	longAddress := strings.Repeat("l", 64) + "@" + strings.Repeat("d", 63) + "." + strings.Repeat("e", 63) + "." + strings.Repeat("f", 61)
	name := strings.Repeat("😀", 200)
	client, err := NewSMTP(Config{Host: host, Port: port, From: longAddress, FromName: name, TLSMode: TLSPlain, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	message := notifications.Message{
		To: longAddress, ToName: name, Subject: "New work assigned: " + strings.Repeat("😀", 60) + "…",
		TextBody: "Assignment reason: " + strings.Repeat("😀", 1000) + "\n.\n", MessageID: "<notification-unicode-boundary@example.test>",
	}
	if err = client.Send(context.Background(), message); err != nil {
		t.Fatalf("send error=%v maximum DATA line=%d", err, server.MaximumObservedLine())
	}
	payload := server.Message(t)
	assertMIMEMessage(t, payload, client.from, &mail.Address{Name: name, Address: longAddress}, message)
}

func TestSMTPValidatesEffectiveDisplayNames(t *testing.T) {
	accepted := []string{strings.Repeat("a", 200), strings.Repeat("é", 200), strings.Repeat("😀", 200)}
	for _, name := range accepted {
		if _, err := NewSMTP(Config{Host: "127.0.0.1", Port: 1025, From: "work@example.test", FromName: name, TLSMode: TLSPlain, Timeout: time.Second}); err != nil {
			t.Fatalf("accepted %d-rune name: %v", len([]rune(name)), err)
		}
	}
	for _, name := range []string{strings.Repeat("a", 201), "bad\x00name", "bad\tname", "bad\nname", "bad\x7fname", "bad\u0085name", string([]byte{'b', 0xff})} {
		if _, err := NewSMTP(Config{Host: "127.0.0.1", Port: 1025, From: "work@example.test", FromName: name, TLSMode: TLSPlain, Timeout: time.Second}); err == nil {
			t.Fatalf("invalid sender name %q was accepted", name)
		}
	}
	if _, err := NewSMTP(Config{Host: "127.0.0.1", Port: 1025, From: "=?utf-8?q?bad=00name?= <work@example.test>", TLSMode: TLSPlain, Timeout: time.Second}); err == nil {
		t.Fatal("invalid embedded sender name was accepted")
	}
}

type smtpServer struct {
	listener              net.Listener
	mu                    sync.Mutex
	message               string
	rcptResponse          string
	maximumDataLineOctets int
	maximumObservedLine   int
	dataAccepted          chan struct{}
	releaseDataResponse   chan struct{}
	done                  chan struct{}
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
				if strings.HasPrefix(dataLine, "..") {
					dataLine = dataLine[1:]
				}
				content := strings.TrimSuffix(dataLine, "\r\n")
				server.mu.Lock()
				if len(content) > server.maximumObservedLine {
					server.maximumObservedLine = len(content)
				}
				server.mu.Unlock()
				lines = append(lines, dataLine)
			}
			server.mu.Lock()
			server.message = strings.Join(lines, "")
			server.mu.Unlock()
			if server.dataAccepted != nil {
				close(server.dataAccepted)
			}
			if server.releaseDataResponse != nil {
				<-server.releaseDataResponse
			}
			server.mu.Lock()
			tooLong := server.maximumDataLineOctets > 0 && server.maximumObservedLine > server.maximumDataLineOctets
			server.mu.Unlock()
			if tooLong {
				write("552 DATA line too long\r\n")
			} else {
				write("250 accepted\r\n")
			}
		case command == "QUIT":
			write("221 bye\r\n")
			return
		default:
			write("250 OK\r\n")
		}
	}
}
func (server *smtpServer) MaximumObservedLine() int {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.maximumObservedLine
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
	if server.releaseDataResponse != nil {
		select {
		case <-server.releaseDataResponse:
		default:
			close(server.releaseDataResponse)
		}
	}
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

const maximumSMTPResponseWriteSlack = 64 << 10

type oversizedResponseSMTPServer struct {
	listener net.Listener
	done     chan struct{}
	mu       sync.Mutex
	written  int
}

func newOversizedResponseSMTPServer(t *testing.T, mode TLSMode, stage string, multiline, auth bool) *oversizedResponseSMTPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &oversizedResponseSMTPServer{listener: listener, done: make(chan struct{})}
	certificate := testCertificate(t)
	go server.serve(mode, stage, multiline, auth, certificate)
	return server
}

func (server *oversizedResponseSMTPServer) Address(t *testing.T) (string, int) {
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

func (server *oversizedResponseSMTPServer) serve(mode TLSMode, oversizedStage string, multiline, auth bool, certificate tls.Certificate) {
	defer close(server.done)
	connection, err := server.listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = connection.Close() }()
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	encrypted := false
	if mode == TLSImplicit {
		tlsConnection := tls.Server(connection, tlsConfig)
		if err = tlsConnection.Handshake(); err != nil {
			return
		}
		connection = tlsConnection
		encrypted = true
	}
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	write := func(value string) bool {
		count, writeErr := io.WriteString(writer, value)
		server.mu.Lock()
		server.written += count
		server.mu.Unlock()
		return writeErr == nil && writer.Flush() == nil
	}
	writeOversized := func(code string) bool {
		var response string
		if multiline {
			response = code + "-" + strings.Repeat("x", maximumSMTPResponseBytes/2) + "\r\n" + code + " " + strings.Repeat("y", maximumSMTPResponseBytes/2) + "\r\n"
		} else {
			response = code + " " + strings.Repeat("x", maximumSMTPResponseBytes+1) + "\r\n"
		}
		return write(response)
	}
	writeCumulativePart := func(code, suffix string) bool {
		const partBytes = maximumSMTPResponseBytes * 3 / 5
		prefix := code + "-"
		ending := "\r\n" + code + " " + suffix + "\r\n"
		return write(prefix + strings.Repeat("c", partBytes-len(prefix)-len(ending)) + ending)
	}
	if oversizedStage == "greeting" {
		_ = writeOversized("220")
		return
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
			if oversizedStage == "cumulative-starttls" {
				if !encrypted {
					if !writeCumulativePart("250", "STARTTLS") {
						return
					}
				} else {
					_ = writeCumulativePart("250", "test")
				}
				continue
			}
			if (oversizedStage == "ehlo" && !encrypted) || (oversizedStage == "encrypted-ehlo" && encrypted) {
				_ = writeOversized("250")
				return
			}
			if mode == TLSSTARTTLS && !encrypted {
				if !write("250-test\r\n250 STARTTLS\r\n") {
					return
				}
			} else if auth {
				if !write("250-test\r\n250 AUTH PLAIN\r\n") {
					return
				}
			} else if !write("250 test\r\n") {
				return
			}
		case command == "STARTTLS" && mode == TLSSTARTTLS && !encrypted:
			if oversizedStage == "starttls" {
				_ = writeOversized("220")
				return
			}
			if !write("220 ready for TLS\r\n") {
				return
			}
			tlsConnection := tls.Server(connection, tlsConfig)
			if err = tlsConnection.Handshake(); err != nil {
				return
			}
			connection = tlsConnection
			reader = bufio.NewReader(connection)
			writer = bufio.NewWriter(connection)
			encrypted = true
		case strings.HasPrefix(command, "AUTH PLAIN"):
			if oversizedStage == "auth" {
				_ = writeOversized("235")
				return
			}
			if !write("235 authenticated\r\n") {
				return
			}
		case strings.HasPrefix(command, "MAIL FROM"):
			if oversizedStage == "mail" {
				_ = writeOversized("250")
				return
			}
			if !write("250 OK\r\n") {
				return
			}
		case strings.HasPrefix(command, "RCPT TO"):
			if oversizedStage == "rcpt" {
				_ = writeOversized("250")
				return
			}
			if !write("250 OK\r\n") {
				return
			}
		case command == "DATA":
			if oversizedStage == "data" {
				_ = writeOversized("354")
				return
			}
			if !write("354 End data\r\n") {
				return
			}
			for {
				dataLine, dataErr := reader.ReadString('\n')
				if dataErr != nil {
					return
				}
				if dataLine == ".\r\n" {
					break
				}
			}
			if oversizedStage == "post-data" {
				_ = writeOversized("250")
				return
			}
			if !write("250 accepted\r\n") {
				return
			}
		case command == "QUIT":
			if oversizedStage == "quit" {
				_ = write("221 " + strings.Repeat("q", maximumSMTPResponseBytes) + "\r\n")
				return
			}
			_ = write("221 bye\r\n")
			return
		}
	}
}

func (server *oversizedResponseSMTPServer) BytesWritten() int {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.written
}

func (server *oversizedResponseSMTPServer) Close() {
	_ = server.listener.Close()
	select {
	case <-server.done:
	case <-time.After(time.Second):
	}
}

func assertSMTPMessage(t *testing.T, got string, want notifications.Message) {
	t.Helper()
	to, err := mail.ParseAddress(want.To)
	if err != nil {
		t.Fatal(err)
	}
	to.Name = want.ToName
	assertMIMEMessage(t, got, nil, to, want)
}

func assertMIMEMessage(t *testing.T, payload string, wantFrom, wantTo *mail.Address, want notifications.Message) {
	t.Helper()
	if strings.ReplaceAll(payload, "\r\n", "") != strings.ReplaceAll(strings.ReplaceAll(payload, "\r\n", ""), "\n", "") || strings.Contains(strings.ReplaceAll(payload, "\r\n", ""), "\r") {
		t.Fatal("message contains a bare CR or LF")
	}
	if maximum := maximumPhysicalLineLength(payload); maximum > 998 {
		t.Fatalf("maximum serialized line=%d octets, want <=998", maximum)
	}
	parsed, err := mail.ReadMessage(strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if transfer := parsed.Header.Get("Content-Transfer-Encoding"); !strings.EqualFold(transfer, "quoted-printable") {
		t.Fatalf("Content-Transfer-Encoding=%q", transfer)
	}
	mediaType, parameters, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/plain" || !strings.EqualFold(parameters["charset"], "UTF-8") {
		t.Fatalf("Content-Type=%q media=%q parameters=%v error=%v", parsed.Header.Get("Content-Type"), mediaType, parameters, err)
	}
	decodedSubject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || decodedSubject != want.Subject {
		t.Fatalf("subject=%q error=%v want=%q", decodedSubject, err, want.Subject)
	}
	if parsed.Header.Get("Message-ID") != want.MessageID {
		t.Fatalf("Message-ID=%q want=%q", parsed.Header.Get("Message-ID"), want.MessageID)
	}
	if wantFrom != nil {
		gotFrom, parseErr := mail.ParseAddress(parsed.Header.Get("From"))
		if parseErr != nil || gotFrom.Name != wantFrom.Name || gotFrom.Address != wantFrom.Address {
			t.Fatalf("From=%#v error=%v want=%#v", gotFrom, parseErr, wantFrom)
		}
	}
	gotTo, err := mail.ParseAddress(parsed.Header.Get("To"))
	if err != nil || gotTo.Name != wantTo.Name || gotTo.Address != wantTo.Address {
		t.Fatalf("To=%#v error=%v want=%#v", gotTo, err, wantTo)
	}
	decodedBody, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	if err != nil {
		t.Fatal(err)
	}
	wantBody := normalizeCRLF(want.TextBody)
	gotBody := string(decodedBody)
	if !strings.HasSuffix(wantBody, "\r\n") && gotBody == wantBody+"\r\n" {
		gotBody = strings.TrimSuffix(gotBody, "\r\n")
	}
	if gotBody != wantBody {
		t.Fatalf("decoded body length=%d want=%d", len(decodedBody), len(wantBody))
	}
}

func maximumPhysicalLineLength(payload string) int {
	maximum := 0
	for _, line := range strings.Split(payload, "\r\n") {
		if len(line) > maximum {
			maximum = len(line)
		}
	}
	return maximum
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
