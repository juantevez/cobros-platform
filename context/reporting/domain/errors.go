package domain

import "errors"

// ErrInvalidFact indica que un evento entrante no tiene los campos mínimos
// (tenant o identificador) para proyectarse como un hecho del read-model.
var ErrInvalidFact = errors.New("reporting: invalid fact: missing tenant or id")

// ErrInvalidRange indica que el rango temporal pedido es inválido (from > to).
var ErrInvalidRange = errors.New("reporting: invalid time range: 'from' must be before 'to'")
