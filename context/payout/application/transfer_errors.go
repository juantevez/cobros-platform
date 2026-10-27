package application

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
)

// isIndeterminate clasifica errores del adaptador bancario en dos grupos:
//
//   - INDETERMINADOS (timeout, cancelación del request, caída de red):
//     NO sabemos si el banco acreditó los fondos. Deben llevar el payout a
//     StatusUnknown y resolverse por reconciliación — nunca a Failed, porque
//     la reversa contable sería incorrecta si el dinero efectivamente salió.
//
//   - RECHAZOS CONOCIDOS (respuesta explícita de rechazo del banco):
//     fallo definitivo → Fail + reversa del asiento.
//
// El criterio es estructural (tipos de error estándar) con un fallback por
// mensajes típicos de las librerías HTTP/sql al perder la conexión.
func isIndeterminate(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, hint := range []string{"connection refused", "no such host", "broken pipe", "network is unreachable", "i/o timeout", "unexpected eof", "context deadline exceeded"} {
		if strings.Contains(msg, hint) {
			return true
		}
	}
	return false
}
