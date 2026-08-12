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
		return notifications.Permanent(errors.New("SMTP sender is not initialized"))
	}
	if err := validateMessage(message); err != nil {
		return notifications.Permanent(err)
	}
	to, err := mail.ParseAddress(message.To)
	if err != nil {
		return notifications.Permanent(fmt.Errorf("parse recipient: %w", err))
	}
	if message.ToName != "" {
		to.Name = message.ToName
	}
	ctx, cancel := context.WithTimeout(ctx, sender.config.Timeout)
	defer cancel()
	address := net.JoinHostPort(sender.config.Host, strconv.Itoa(sender.config.Port))
	raw, err := (&net.Dialer{Timeout: sender.config.Timeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return classify(fmt.Errorf("dial SMTP server: %w", err))
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(deadline)
	}
	connection := raw
	if sender.config.TLSMode == TLSImplicit {
		tlsConnection := tls.Client(raw, sender.tlsConfig())
		if err = tlsConnection.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return classify(err)
		}
		connection = tlsConnection
	}
	client, err := smtp.NewClient(connection, sender.config.Host)
	if err != nil {
		_ = connection.Close()
		return classify(err)
	}
	defer func() { _ = client.Close() }()
	if sender.config.TLSMode == TLSSTARTTLS {
		supported, _ := client.Extension("STARTTLS")
		if !supported {
			return notifications.Permanent(errors.New("SMTP server does not advertise STARTTLS"))
		}
		if err = client.StartTLS(sender.tlsConfig()); err != nil {
			return classify(err)
		}
	}
	if sender.config.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", sender.config.Username, sender.config.Password, sender.config.Host)); err != nil {
			return classify(err)
		}
	}
	if err = client.Mail(sender.from.Address); err != nil {
		return classify(err)
	}
	if err = client.Rcpt(to.Address); err != nil {
		return classify(err)
	}
	writer, err := client.Data()
	if err != nil {
		return classify(err)
	}
	payload, err := buildMessage(sender.from, to, message)
	if err != nil {
		_ = writer.Close()
		return notifications.Permanent(err)
	}
	if _, err = io.Copy(writer, strings.NewReader(payload)); err != nil {
		_ = writer.Close()
		return classify(err)
	}
	if err = writer.Close(); err != nil {
		return classify(err)
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
func classify(err error) error {
	if err == nil {
		return nil
	}
	var protocolError *textproto.Error
	if errors.As(err, &protocolError) {
		if protocolError.Code >= 500 {
			return notifications.Permanent(err)
		}
		return notifications.Retryable(err)
	}
	var networkError net.Error
	if errors.As(err, &networkError) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return notifications.Retryable(err)
	}
	return notifications.Retryable(err)
}
