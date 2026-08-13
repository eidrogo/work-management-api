package email

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/eidrogo/work-management-api/internal/notifications"
)

type TLSMode string

const (
	TLSPlain    TLSMode = "plain"
	TLSSTARTTLS TLSMode = "starttls"
	TLSImplicit TLSMode = "tls"
)

type Config struct {
	Host, Username, Password, From, FromName, TLSServerName string
	Port                                                    int
	TLSMode                                                 TLSMode
	TLSInsecureSkipVerify                                   bool
	Timeout                                                 time.Duration
}
type SMTP struct {
	config Config
	from   *mail.Address
}

func NewSMTP(config Config) (*SMTP, error) {
	config.Host = strings.TrimSpace(config.Host)
	config.From = strings.TrimSpace(config.From)
	if config.Host == "" || config.Port < 1 || config.Port > 65535 || config.Timeout <= 0 {
		return nil, fmt.Errorf("invalid SMTP configuration")
	}
	if (config.Username == "") != (config.Password == "") {
		return nil, fmt.Errorf("SMTP username and password must be configured together")
	}
	switch config.TLSMode {
	case TLSPlain, TLSSTARTTLS, TLSImplicit:
	default:
		return nil, fmt.Errorf("unsupported SMTP TLS mode %q", config.TLSMode)
	}
	from, err := mail.ParseAddress(config.From)
	if err != nil {
		return nil, fmt.Errorf("parse SMTP from address: %w", err)
	}
	if config.FromName != "" {
		from.Name = config.FromName
	}
	if config.TLSServerName == "" {
		config.TLSServerName = config.Host
	}
	return &SMTP{config: config, from: from}, nil
}
func (sender *SMTP) Send(ctx context.Context, message notifications.Message) error {
	if sender == nil {
		return notifications.Permanent(notifications.FailureSMTPConfiguration, errors.New("SMTP sender is not initialized"))
	}
	if err := validateMessage(message); err != nil {
		return notifications.Permanent(notifications.FailureSMTPConfiguration, err)
	}
	to, err := mail.ParseAddress(message.To)
	if err != nil {
		return notifications.Permanent(notifications.FailureSMTPConfiguration, fmt.Errorf("parse recipient: %w", err))
	}
	if message.ToName != "" {
		to.Name = message.ToName
	}
	ctx, cancel := context.WithTimeout(ctx, sender.config.Timeout)
	defer cancel()
	address := net.JoinHostPort(sender.config.Host, strconv.Itoa(sender.config.Port))
	raw, err := (&net.Dialer{Timeout: sender.config.Timeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return classify(smtpStageConnection, fmt.Errorf("dial SMTP server: %w", err))
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(deadline)
	}
	stopCancellationInterrupt := context.AfterFunc(ctx, func() { _ = raw.SetDeadline(time.Now()) })
	defer stopCancellationInterrupt()
	connection := raw
	if sender.config.TLSMode == TLSImplicit {
		tlsConnection := tls.Client(raw, sender.tlsConfig())
		if err = tlsConnection.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return classify(smtpStageTLS, err)
		}
		connection = tlsConnection
	}
	client, err := smtp.NewClient(connection, sender.config.Host)
	if err != nil {
		_ = connection.Close()
		return classify(smtpStageProtocol, err)
	}
	defer func() { _ = client.Close() }()
	if sender.config.TLSMode == TLSSTARTTLS {
		supported, _ := client.Extension("STARTTLS")
		if !supported {
			return notifications.Permanent(notifications.FailureSMTPConfiguration, errors.New("SMTP server does not advertise STARTTLS"))
		}
		if err = client.StartTLS(sender.tlsConfig()); err != nil {
			return classify(smtpStageTLS, err)
		}
	}
	if sender.config.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", sender.config.Username, sender.config.Password, sender.config.Host)); err != nil {
			return classify(smtpStageAuthentication, err)
		}
	}
	if err = client.Mail(sender.from.Address); err != nil {
		return classify(smtpStageMessage, err)
	}
	if err = client.Rcpt(to.Address); err != nil {
		return classify(smtpStageRecipient, err)
	}
	writer, err := client.Data()
	if err != nil {
		return classify(smtpStageMessage, err)
	}
	payload, err := buildMessage(sender.from, to, message)
	if err != nil {
		_ = writer.Close()
		return notifications.Permanent(notifications.FailureSMTPConfiguration, err)
	}
	if _, err = io.Copy(writer, strings.NewReader(payload)); err != nil {
		_ = writer.Close()
		return classify(smtpStageConnection, err)
	}
	if err = writer.Close(); err != nil {
		return classify(smtpStageMessage, err)
	}
	_ = client.Quit()
	return nil
}
func (sender *SMTP) tlsConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, ServerName: sender.config.TLSServerName, InsecureSkipVerify: sender.config.TLSInsecureSkipVerify}
}
func validateMessage(message notifications.Message) error {
	if strings.TrimSpace(message.To) == "" || strings.TrimSpace(message.Subject) == "" || strings.TrimSpace(message.MessageID) == "" {
		return errors.New("recipient, subject, and message id are required")
	}
	for name, value := range map[string]string{"recipient name": message.ToName, "subject": message.Subject, "message id": message.MessageID} {
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%s contains a header newline", name)
		}
	}
	return nil
}
func buildMessage(from, to *mail.Address, message notifications.Message) (string, error) {
	var builder strings.Builder
	writer := bufio.NewWriter(&builder)
	headers := [][2]string{{"Date", time.Now().UTC().Format(time.RFC1123Z)}, {"Message-ID", message.MessageID}, {"From", from.String()}, {"To", to.String()}, {"Subject", mime.QEncoding.Encode("UTF-8", message.Subject)}, {"MIME-Version", "1.0"}, {"Content-Type", `text/plain; charset="UTF-8"`}, {"Content-Transfer-Encoding", "8bit"}}
	for _, header := range headers {
		if strings.ContainsAny(header[1], "\r\n") {
			return "", fmt.Errorf("invalid %s header", header[0])
		}
		if _, err := fmt.Fprintf(writer, "%s: %s\r\n", header[0], header[1]); err != nil {
			return "", err
		}
	}
	_, _ = writer.WriteString("\r\n" + normalizeCRLF(message.TextBody))
	if err := writer.Flush(); err != nil {
		return "", err
	}
	return builder.String(), nil
}
func normalizeCRLF(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}

type smtpStage string

const (
	smtpStageConnection     smtpStage = "connection"
	smtpStageTLS            smtpStage = "tls"
	smtpStageAuthentication smtpStage = "authentication"
	smtpStageRecipient      smtpStage = "recipient"
	smtpStageMessage        smtpStage = "message"
	smtpStageProtocol       smtpStage = "protocol"
)

func classify(stage smtpStage, err error) error {
	if err == nil {
		return nil
	}
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
		return notifications.Retryable(notifications.FailureSMTPTimeout, err)
	}
	var protocolError *textproto.Error
	if errors.As(err, &protocolError) {
		if protocolError.Code >= 400 && protocolError.Code < 500 {
			return notifications.Retryable(notifications.FailureSMTPTemporary, err)
		}
		if protocolError.Code >= 500 {
			switch stage {
			case smtpStageAuthentication:
				return notifications.Permanent(notifications.FailureSMTPAuthentication, err)
			case smtpStageRecipient:
				return notifications.Permanent(notifications.FailureSMTPRecipientRejected, err)
			case smtpStageMessage:
				return notifications.Permanent(notifications.FailureSMTPMessageRejected, err)
			case smtpStageTLS:
				return notifications.Permanent(notifications.FailureSMTPTLS, err)
			default:
				return notifications.Retryable(notifications.FailureSMTPProtocol, err)
			}
		}
		return notifications.Retryable(notifications.FailureSMTPProtocol, err)
	}
	if stage == smtpStageTLS {
		return notifications.Retryable(notifications.FailureSMTPTLS, err)
	}
	if errors.As(err, &networkError) || errors.Is(err, context.Canceled) || stage == smtpStageConnection {
		return notifications.Retryable(notifications.FailureSMTPConnection, err)
	}
	return notifications.Retryable(notifications.FailureSMTPProtocol, err)
}
