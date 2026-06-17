// Package email provee implementaciones del puerto EmailSender.
package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/smtp"
)

// SMTPSender envía emails via SMTP con autenticación.
// Soporta TLS (SMTPS en puerto 465) y STARTTLS (puerto 587).
type SMTPSender struct {
	host     string
	port     int
	username string
	password string
	from     string
	useTLS   bool
}

type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	UseTLS   bool // true para SMTPS (465), false para STARTTLS (587)
}

func NewSMTPSender(cfg SMTPConfig) *SMTPSender {
	return &SMTPSender{
		host:     cfg.Host,
		port:     cfg.Port,
		username: cfg.Username,
		password: cfg.Password,
		from:     cfg.From,
		useTLS:   cfg.UseTLS,
	}
}

func (s *SMTPSender) Send(_ context.Context, to, subject, body string) error {
	msg := buildMessage(s.from, to, subject, body)
	addr := fmt.Sprintf("%s:%d", s.host, s.port)

	if s.useTLS {
		return s.sendTLS(addr, to, msg)
	}
	return s.sendSTARTTLS(addr, to, msg)
}

func (s *SMTPSender) sendSTARTTLS(addr, to string, msg []byte) error {
	auth := smtp.PlainAuth("", s.username, s.password, s.host)
	return smtp.SendMail(addr, auth, s.from, []string{to}, msg)
}

func (s *SMTPSender) sendTLS(addr, to string, msg []byte) error {
	tlsCfg := &tls.Config{ServerName: s.host}
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("smtp tls dial: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer client.Close()

	auth := smtp.PlainAuth("", s.username, s.password, s.host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := client.Mail(s.from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	defer w.Close()
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	return nil
}

// ── LogSender ─────────────────────────────────────────────────────────────────

// LogSender implementa EmailSender logueando los emails en vez de enviarlos.
// Usar en desarrollo local y tests.
type LogSender struct {
	logger *slog.Logger
}

func NewLogSender(logger *slog.Logger) *LogSender {
	return &LogSender{logger: logger}
}

func (s *LogSender) Send(_ context.Context, to, subject, _ string) error {
	s.logger.Info("📧 [DEV] Email simulado",
		"to", to,
		"subject", subject,
	)
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func buildMessage(from, to, subject, body string) []byte {
	return []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/html; charset=UTF-8\r\nMIME-Version: 1.0\r\n\r\n%s",
		from, to, subject, body,
	))
}
