package application

import "errors"

// ErrBankRejected: el banco rechazó la transferencia de forma definitiva
// (cuenta inexistente o cerrada, titular distinto, …). Los adaptadores lo
// envuelven con %w. Solo este error lleva el payout a failed y revierte el
// asiento.
//
// Cualquier otro error de Transfer (timeout, red, 5xx, un error imprevisto del
// SDK) es un resultado DESCONOCIDO: el banco pudo haber acreditado los fondos,
// así que el payout queda unknown sin reversa hasta que se resuelva. Antes era
// al revés (se reconocía el timeout por tipo o mensaje y todo lo demás era
// rechazo): un error no previsto revertía un desembolso que el banco pagó.
var ErrBankRejected = errors.New("bank rejected the transfer")
