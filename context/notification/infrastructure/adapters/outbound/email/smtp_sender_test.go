package email

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestBuildMessage(t *testing.T) {
	msg := string(buildMessage("from@x.com", "to@x.com", "Asunto", "<p>hola</p>"))

	for _, want := range []string{
		"From: from@x.com",
		"To: to@x.com",
		"Subject: Asunto",
		"Content-Type: text/html; charset=UTF-8",
		"MIME-Version: 1.0",
		"<p>hola</p>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
	// El header y el body van separados por una línea en blanco.
	if !strings.Contains(msg, "\r\n\r\n<p>hola</p>") {
		t.Errorf("header/body separator missing:\n%s", msg)
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
