package email

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/eidrogo/work-management-api/internal/notifications"
)

type TLSMode string

const (
	TLSPlain                  TLSMode = "plain"
	TLSSTARTTLS               TLSMode = "starttls"
	TLSImplicit               TLSMode = "tls"
	MaxDisplayNameRunes               = 200
	maximumMessageLineOctets          = 998
	preferredHeaderLineOctets         = 78
	maximumEncodedWordBytes           = 45
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
	if err = ValidateDisplayName(from.Name); err != nil {
		return nil, fmt.Errorf("invalid SMTP from display name: %w", err)
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
	if err = ValidateDisplayName(to.Name); err != nil {
		return notifications.Permanent(notifications.FailureSMTPConfiguration, fmt.Errorf("invalid recipient display name: %w", err))
	}
	payload, err := buildMessage(sender.from, to, message)
	if err != nil {
		return notifications.Permanent(notifications.FailureSMTPConfiguration, err)
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
	if err := ValidateDisplayName(message.ToName); err != nil {
		return fmt.Errorf("invalid recipient display name: %w", err)
	}
	if !utf8.ValidString(message.Subject) {
		return fmt.Errorf("subject is not valid UTF-8")
	}
	if strings.ContainsAny(message.Subject, "\r\n") {
		return fmt.Errorf("subject contains a header newline")
	}
	if !utf8.ValidString(message.MessageID) {
		return fmt.Errorf("message id is not valid UTF-8")
	}
	for _, character := range message.MessageID {
		if unicode.IsControl(character) {
			return fmt.Errorf("message id contains a header control character")
		}
	}
	return nil
}

func ValidateDisplayName(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("display name must be valid UTF-8")
	}
	if utf8.RuneCountInString(value) > MaxDisplayNameRunes {
		return fmt.Errorf("display name must not exceed %d characters", MaxDisplayNameRunes)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("display name must not contain control characters")
		}
	}
	return nil
}

func buildMessage(from, to *mail.Address, message notifications.Message) (string, error) {
	if from == nil || to == nil {
		return "", fmt.Errorf("sender and recipient addresses are required")
	}
	if err := validateMessage(message); err != nil {
		return "", err
	}
	fromComponents, err := addressHeaderComponents(from)
	if err != nil {
		return "", fmt.Errorf("invalid From header: %w", err)
	}
	toComponents, err := addressHeaderComponents(to)
	if err != nil {
		return "", fmt.Errorf("invalid To header: %w", err)
	}
	var builder strings.Builder
	writer := bufio.NewWriter(&builder)
	headers := []struct {
		name       string
		components []string
	}{
		{name: "Date", components: []string{time.Now().UTC().Format(time.RFC1123Z)}},
		{name: "Message-ID", components: []string{message.MessageID}},
		{name: "From", components: fromComponents},
		{name: "To", components: toComponents},
		{name: "Subject", components: subjectHeaderComponents(message.Subject)},
		{name: "MIME-Version", components: []string{"1.0"}},
		{name: "Content-Type", components: []string{`text/plain; charset="UTF-8"`}},
		{name: "Content-Transfer-Encoding", components: []string{"quoted-printable"}},
	}
	for _, header := range headers {
		if err := writeFoldedHeader(writer, header.name, header.components); err != nil {
			return "", err
		}
	}
	if _, err := writer.WriteString("\r\n"); err != nil {
		return "", err
	}
	bodyWriter := quotedprintable.NewWriter(writer)
	if _, err := io.WriteString(bodyWriter, normalizeCRLF(message.TextBody)); err != nil {
		return "", err
	}
	if err := bodyWriter.Close(); err != nil {
		return "", err
	}
	if err := writer.Flush(); err != nil {
		return "", err
	}
	payload := builder.String()
	if err := validatePhysicalLines(payload); err != nil {
		return "", err
	}
	return payload, nil
}

func addressHeaderComponents(address *mail.Address) ([]string, error) {
	if err := ValidateDisplayName(address.Name); err != nil {
		return nil, err
	}
	mailbox := (&mail.Address{Address: address.Address}).String()
	if strings.ContainsAny(mailbox, "\r\n") {
		return nil, fmt.Errorf("mailbox contains a header newline")
	}
	if address.Name == "" {
		return []string{mailbox}, nil
	}
	return append(encodedPhraseWords(address.Name), mailbox), nil
}

func encodedPhraseWords(value string) []string {
	var words []string
	var chunk strings.Builder
	flush := func() {
		if chunk.Len() == 0 {
			return
		}
		words = append(words, "=?UTF-8?B?"+base64.StdEncoding.EncodeToString([]byte(chunk.String()))+"?=")
		chunk.Reset()
	}
	for _, character := range value {
		if chunk.Len()+utf8.RuneLen(character) > maximumEncodedWordBytes {
			flush()
		}
		chunk.WriteRune(character)
	}
	flush()
	return words
}

func subjectHeaderComponents(value string) []string {
	for _, character := range value {
		if unicode.IsControl(character) {
			return encodedPhraseWords(value)
		}
	}
	encoded := mime.QEncoding.Encode("UTF-8", value)
	if encoded != value {
		return strings.Split(encoded, " ")
	}
	if strings.TrimSpace(value) == value && !strings.Contains(value, "  ") && !strings.ContainsRune(value, '\t') {
		return strings.Split(value, " ")
	}
	return []string{value}
}

func writeFoldedHeader(writer *bufio.Writer, name string, components []string) error {
	if len(components) == 0 {
		return fmt.Errorf("invalid %s header", name)
	}
	line := name + ":"
	hasValue := false
	flush := func() error {
		if len(line) > maximumMessageLineOctets {
			return fmt.Errorf("%s header line exceeds %d octets", name, maximumMessageLineOctets)
		}
		_, err := writer.WriteString(line + "\r\n")
		return err
	}
	for _, component := range components {
		if component == "" || strings.ContainsAny(component, "\r\n") {
			return fmt.Errorf("invalid %s header", name)
		}
		separator := " "
		candidate := line + separator + component
		if len(candidate) <= preferredHeaderLineOctets {
			line = candidate
			hasValue = true
			continue
		}
		if hasValue {
			if err := flush(); err != nil {
				return err
			}
		} else if err := flush(); err != nil {
			return err
		}
		line = " " + component
		hasValue = true
		if len(line) > maximumMessageLineOctets {
			return fmt.Errorf("%s header component exceeds %d octets", name, maximumMessageLineOctets)
		}
	}
	return flush()
}

func validatePhysicalLines(payload string) error {
	withoutCRLF := strings.ReplaceAll(payload, "\r\n", "")
	if strings.ContainsAny(withoutCRLF, "\r\n") {
		return fmt.Errorf("message contains a non-CRLF line ending")
	}
	for _, line := range strings.Split(payload, "\r\n") {
		if len(line) > maximumMessageLineOctets {
			return fmt.Errorf("message line exceeds %d octets", maximumMessageLineOctets)
		}
	}
	return nil
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
