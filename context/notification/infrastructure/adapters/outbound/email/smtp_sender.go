// Package email provee implementaciones del puerto EmailSender.
package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net/smtp"
	"strings"
	"time"
)

// ErrHeaderInjection: un valor de header (From/To/Subject) trae un salto de
// línea, que permitiría inyectar headers o destinatarios extra.
var ErrHeaderInjection = errors.New("email header contains a line break")

// SMTPSender envía emails via SMTP con autenticación.
// Soporta TLS (SMTPS en puerto 465) y STARTTLS (puerto 587).
type SMTPSender struct {
	host     string
	port     int
	username string
	password string
	from     string
	useTLS   bool

	// tlsConfig permite inyectar una config TLS en tests (ej: con RootCAs de
	// un cert self-signed). En producción queda nil y se usa la config por
	// defecto que verifica contra las CAs del sistema.
	tlsConfig *tls.Config

	now func() time.Time // header Date; inyectable en tests
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
		now:      time.Now,
	}
}

func (s *SMTPSender) Send(_ context.Context, to, subject, body string) error {
	msg, err := buildMessage(s.from, to, subject, body, s.now())
	if err != nil {
		return err
	}
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
	tlsCfg := s.tlsConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{ServerName: s.host}
	}
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
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	// Close termina el DATA y trae la respuesta del servidor: ahí acepta o
	// rechaza el mensaje. Ignorarlo registraría como enviado un email rechazado.
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp data rejected: %w", err)
	}
	return client.Quit()
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

// buildMessage arma el mensaje RFC 5322. Rechaza saltos de línea en los
// headers (inyección), codifica el Subject según RFC 2047 si no es ASCII
// (los templates usan emojis y acentos) y envía el body HTML en
// quoted-printable (líneas ≤ 76, sin depender de un transporte 8-bit).
func buildMessage(from, to, subject, body string, date time.Time) ([]byte, error) {
	for name, v := range map[string]string{"From": from, "To": to, "Subject": subject} {
		if strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("%w: %s", ErrHeaderInjection, name)
		}
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", date.Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")

	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(body)); err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}
	return b.Bytes(), nil
}
