package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestBuildMessage(t *testing.T) {
	date := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	t.Run("ascii message", func(t *testing.T) {
		raw, err := buildMessage("from@x.com", "to@x.com", "Asunto", "<p>hola</p>", date)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		msg := string(raw)
		for _, want := range []string{
			"From: from@x.com\r\n",
			"To: to@x.com\r\n",
			"Subject: Asunto\r\n",
			"Date: Fri, 30 Oct 2026 12:00:00 +0000\r\n",
			"MIME-Version: 1.0\r\n",
			"Content-Type: text/html; charset=UTF-8\r\n",
			"Content-Transfer-Encoding: quoted-printable\r\n",
			"\r\n\r\n<p>hola</p>",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message missing %q:\n%s", want, msg)
			}
		}
	})

	t.Run("non-ascii subject is RFC 2047 encoded and decodes back", func(t *testing.T) {
		subject := "✅ Pago recibido: 1500.00 ARS"
		raw, _ := buildMessage("from@x.com", "to@x.com", subject, "x", date)
		m, err := mail.ReadMessage(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("not a valid RFC 5322 message: %v", err)
		}
		hdr := m.Header.Get("Subject")
		if !strings.HasPrefix(hdr, "=?utf-8?q?") {
			t.Errorf("subject not encoded: %q", hdr)
		}
		if got, _ := new(mime.WordDecoder).DecodeHeader(hdr); got != subject {
			t.Errorf("decoded subject = %q, want %q", got, subject)
		}
	})

	t.Run("body is quoted-printable and decodes back", func(t *testing.T) {
		body := "<p>Reembolso: <strong>1500.00</strong> — acción=ok</p>" + strings.Repeat("x", 300)
		raw, _ := buildMessage("from@x.com", "to@x.com", "s", body, date)
		m, _ := mail.ReadMessage(bytes.NewReader(raw))
		decoded, err := io.ReadAll(quotedprintable.NewReader(m.Body))
		if err != nil || string(decoded) != body {
			t.Fatalf("body round trip failed: %v\n%q", err, decoded)
		}
		for _, line := range strings.Split(string(raw), "\r\n") {
			if len(line) > 998 {
				t.Errorf("line exceeds SMTP limit: %d chars", len(line))
			}
		}
	})

	t.Run("line breaks in headers are rejected", func(t *testing.T) {
		for _, tc := range []struct{ from, to, subject string }{
			{"from@x.com", "to@x.com\r\nBcc: evil@x.com", "s"},
			{"from@x.com", "to@x.com", "s\nBcc: evil@x.com"},
			{"from@x.com\r\nX: y", "to@x.com", "s"},
		} {
			if _, err := buildMessage(tc.from, tc.to, tc.subject, "b", date); !errors.Is(err, ErrHeaderInjection) {
				t.Errorf("%+v: expected ErrHeaderInjection, got %v", tc, err)
			}
		}
	})
}

func TestSMTPSender_Send_TLS_DataRejected(t *testing.T) {
	// El servidor acepta MAIL/RCPT pero rechaza el mensaje al cerrar el DATA:
	// el envío debe fallar (antes el error del Close se descartaba).
	serverCfg, roots := selfSignedTLS(t)
	srv := startFakeSMTP(t, serverCfg, "DATAEND")
	s := NewSMTPSender(SMTPConfig{Host: srv.host, Port: srv.port, Username: "u", Password: "p", From: "from@x.com", UseTLS: true})
	s.tlsConfig = &tls.Config{ServerName: "127.0.0.1", RootCAs: roots}
	if err := s.Send(context.Background(), "to@x.com", "s", "b"); err == nil {
		t.Fatal("expected error when the server rejects the message at end of DATA")
	}
}

func TestSMTPSender_Send_HeaderInjectionNeverReachesServer(t *testing.T) {
	serverCfg, roots := selfSignedTLS(t)
	srv := startFakeSMTP(t, serverCfg, "")
	s := NewSMTPSender(SMTPConfig{Host: srv.host, Port: srv.port, Username: "u", Password: "p", From: "from@x.com", UseTLS: true})
	s.tlsConfig = &tls.Config{ServerName: "127.0.0.1", RootCAs: roots}
	if err := s.Send(context.Background(), "to@x.com", "s\r\nBcc: evil@x.com", "b"); !errors.Is(err, ErrHeaderInjection) {
		t.Fatalf("expected ErrHeaderInjection, got %v", err)
	}
	if srv.lastMessage() != nil {
		t.Error("nothing should be sent")
	}
}

func TestLogSender_Send(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewLogSender(logger)
	if err := s.Send(context.Background(), "to@x.com", "Asunto", "body"); err != nil {
		t.Fatalf("LogSender.Send should never fail: %v", err)
	}
}

func TestNewSMTPSender(t *testing.T) {
	s := NewSMTPSender(SMTPConfig{
		Host: "smtp.x.com", Port: 587, Username: "u", Password: "p",
		From: "from@x.com", UseTLS: false,
	})
	if s.host != "smtp.x.com" || s.port != 587 || s.from != "from@x.com" || s.useTLS {
		t.Errorf("config not applied: %+v", s)
	}
}

func TestSMTPSender_Send_STARTTLS_DialError(t *testing.T) {
	// Puerto 0 no es conectable → SendMail (STARTTLS path) debe fallar.
	s := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: 0, From: "from@x.com", UseTLS: false})
	if err := s.Send(context.Background(), "to@x.com", "s", "b"); err == nil {
		t.Fatal("expected dial error on unreachable STARTTLS server")
	}
}

func TestSMTPSender_Send_TLS_DialError(t *testing.T) {
	// Puerto 0 no es conectable → tls.Dial debe fallar con el wrap "smtp tls dial".
	s := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: 0, From: "from@x.com", UseTLS: true})
	err := s.Send(context.Background(), "to@x.com", "s", "b")
	if err == nil {
		t.Fatal("expected dial error on unreachable TLS server")
	}
	if !strings.Contains(err.Error(), "smtp tls dial") {
		t.Errorf("error = %v, want smtp tls dial wrap", err)
	}
}

func TestSMTPSender_Send_STARTTLS_Success(t *testing.T) {
	srv := startFakeSMTP(t, nil, "") // servidor de texto plano
	s := NewSMTPSender(SMTPConfig{
		Host: srv.host, Port: srv.port, Username: "u", Password: "p",
		From: "from@x.com", UseTLS: false,
	})

	if err := s.Send(context.Background(), "to@x.com", "Asunto", "<p>hola</p>"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msg := string(srv.lastMessage())
	if !strings.Contains(msg, "Subject: Asunto") || !strings.Contains(msg, "<p>hola</p>") {
		t.Errorf("server did not receive expected message:\n%s", msg)
	}
	if !strings.Contains(msg, "To: to@x.com") {
		t.Errorf("recipient header missing:\n%s", msg)
	}
}

func TestSMTPSender_Send_TLS_Success(t *testing.T) {
	serverCfg, roots := selfSignedTLS(t)
	srv := startFakeSMTP(t, serverCfg, "")

	s := NewSMTPSender(SMTPConfig{
		Host: srv.host, Port: srv.port, Username: "u", Password: "p",
		From: "from@x.com", UseTLS: true,
	})
	// El cliente confía en el cert self-signed vía RootCAs inyectadas.
	s.tlsConfig = &tls.Config{ServerName: "127.0.0.1", RootCAs: roots}

	if err := s.Send(context.Background(), "to@x.com", "Asunto TLS", "<p>seguro</p>"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msg := string(srv.lastMessage())
	if !strings.Contains(msg, "Subject: Asunto TLS") || !strings.Contains(msg, "<p>seguro</p>") {
		t.Errorf("server did not receive expected message over TLS:\n%s", msg)
	}
}

func TestSMTPSender_Send_TLS_ServerRejects(t *testing.T) {
	cases := map[string]string{
		"auth rejected": "AUTH",
		"mail rejected": "MAIL",
		"rcpt rejected": "RCPT",
	}
	for name, rejectCmd := range cases {
		t.Run(name, func(t *testing.T) {
			serverCfg, roots := selfSignedTLS(t)
			srv := startFakeSMTP(t, serverCfg, rejectCmd)

			s := NewSMTPSender(SMTPConfig{
				Host: srv.host, Port: srv.port, Username: "u", Password: "p",
				From: "from@x.com", UseTLS: true,
			})
			s.tlsConfig = &tls.Config{ServerName: "127.0.0.1", RootCAs: roots}

			if err := s.Send(context.Background(), "to@x.com", "s", "b"); err == nil {
				t.Fatalf("expected error when server rejects %s", rejectCmd)
			}
		})
	}
}
