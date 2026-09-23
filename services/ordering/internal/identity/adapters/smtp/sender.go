package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	netsmtp "net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/ports"
)

type Sender struct {
	host               string
	port               int
	from               string
	username           string
	password           string
	allowInsecureLocal bool
}

var _ ports.VerificationMailer = (*Sender)(nil)

func (*Sender) Configured() bool { return true }

func New(host string, port int, from, username, password string, allowInsecureLocal bool) (*Sender, error) {
	if host == "" || port < 1 || port > 65535 || !validAddress(from) {
		return nil, errors.New("invalid SMTP configuration")
	}
	if allowInsecureLocal && host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return nil, errors.New("insecure SMTP is restricted to loopback")
	}
	if !allowInsecureLocal && (username == "" || password == "") {
		return nil, errors.New("SMTP submission requires credentials")
	}
	return &Sender{host: host, port: port, from: from, username: username, password: password, allowInsecureLocal: allowInsecureLocal}, nil
}

func (sender *Sender) SendCode(ctx context.Context, email, code string) error {
	if !validAddress(email) || len(code) != 6 {
		return errors.New("invalid verification message")
	}
	for _, digit := range code {
		if digit < '0' || digit > '9' {
			return errors.New("invalid verification code")
		}
	}
	address := net.JoinHostPort(sender.host, strconv.Itoa(sender.port))
	connection, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("connect SMTP: %w", err)
	}
	defer func() { _ = connection.Close() }()
	deadline := time.Now().Add(10 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return err
	}
	client, err := netsmtp.NewClient(connection, sender.host)
	if err != nil {
		return fmt.Errorf("start SMTP: %w", err)
	}
	defer func() { _ = client.Close() }()
	if !sender.allowInsecureLocal {
		if err := client.StartTLS(&tls.Config{ServerName: sender.host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("secure SMTP: %w", err)
		}
		if err := client.Auth(netsmtp.PlainAuth("", sender.username, sender.password, sender.host)); err != nil {
			return fmt.Errorf("authenticate SMTP: %w", err)
		}
	}
	if err := client.Mail(sender.from); err != nil {
		return fmt.Errorf("set SMTP sender: %w", err)
	}
	if err := client.Rcpt(email); err != nil {
		return fmt.Errorf("set SMTP recipient: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("write SMTP message: %w", err)
	}
	message := "From: " + sender.from + "\r\nTo: " + email +
		"\r\nSubject: Your B46 verification code\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" +
		"Your B46 verification code is " + code + ". It expires in 10 minutes.\r\n"
	if _, err := io.Copy(writer, strings.NewReader(message)); err != nil {
		_ = writer.Close()
		return fmt.Errorf("send SMTP message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish SMTP message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("finish SMTP submission: %w", err)
	}
	return nil
}

func validAddress(value string) bool {
	if strings.ContainsAny(value, "\r\n") {
		return false
	}
	parsed, err := mail.ParseAddress(value)
	return err == nil && parsed.Address == value && parsed.Name == ""
}
