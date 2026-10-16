package email

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP es un servidor SMTP mínimo para tests. Habla lo justo del protocolo
// para que net/smtp complete un envío: EHLO, AUTH PLAIN, MAIL, RCPT, DATA, QUIT.
// No anuncia STARTTLS, de modo que el cliente no intenta upgrade en el path plano.
type fakeSMTP struct {
	host string
	port int
	ln   net.Listener

	// rejectCmd hace que el servidor responda 550 al comando indicado
	// (ej: "RCPT", "AUTH", "MAIL", "DATA") para ejercitar los errores del cliente.
	rejectCmd string

	mu       sync.Mutex
	messages [][]byte
}

// startFakeSMTP levanta el servidor en 127.0.0.1. Si tlsCfg != nil el listener
// es TLS (simula SMTPS en puerto 465). rejectCmd (vacío = ninguno) hace que el
// servidor responda 550 al comando indicado.
func startFakeSMTP(t *testing.T, tlsCfg *tls.Config, rejectCmd string) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if tlsCfg != nil {
		ln = tls.NewListener(ln, tlsCfg)
	}
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	s := &fakeSMTP{host: host, port: port, ln: ln, rejectCmd: rejectCmd}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *fakeSMTP) lastMessage() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		return nil
	}
	return s.messages[len(s.messages)-1]
}

func (s *fakeSMTP) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return // listener cerrado
		}
		go s.handleConn(conn)
	}
}

func (s *fakeSMTP) handleConn(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	fmt.Fprint(conn, "220 mock ESMTP\r\n")

	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		if s.rejectCmd != "" && strings.HasPrefix(cmd, s.rejectCmd) {
			fmt.Fprint(conn, "550 rejected\r\n")
			continue
		}
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			// Anuncia AUTH PLAIN pero NO STARTTLS.
			fmt.Fprint(conn, "250-mock\r\n250 AUTH PLAIN\r\n")
		case strings.HasPrefix(cmd, "AUTH"):
			fmt.Fprint(conn, "235 2.7.0 Authentication successful\r\n")
		case strings.HasPrefix(cmd, "MAIL"):
			fmt.Fprint(conn, "250 OK\r\n")
		case strings.HasPrefix(cmd, "RCPT"):
			fmt.Fprint(conn, "250 OK\r\n")
		case strings.HasPrefix(cmd, "DATA"):
			fmt.Fprint(conn, "354 End data with <CR><LF>.<CR><LF>\r\n")
			var body bytes.Buffer
			for {
				l, err := br.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				body.WriteString(l)
			}
			s.mu.Lock()
			s.messages = append(s.messages, body.Bytes())
			s.mu.Unlock()
			fmt.Fprint(conn, "250 OK\r\n")
		case strings.HasPrefix(cmd, "QUIT"):
			fmt.Fprint(conn, "221 Bye\r\n")
			return
		default:
			fmt.Fprint(conn, "250 OK\r\n")
		}
	}
}

// selfSignedTLS genera un cert self-signed para 127.0.0.1 y retorna la config
// del servidor y un pool de CAs para que el cliente confíe en él.
func selfSignedTLS(t *testing.T) (server *tls.Config, clientRoots *x509.CertPool) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)

	server = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: priv}}}
	return server, pool
}
