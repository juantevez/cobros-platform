package domain

import (
	"encoding/hex"
	"time"
)

// ChainCheckpoint registra la cabeza de la cadena (id + hash de la última
// entrada) en un momento dado. Un checkpoint posterior a una entrada prueba
// que esa entrada existió: si después falta o su hash cambió, la cola de la
// cadena fue truncada o reescrita (algo que el enlace prev_hash por sí solo
// no detecta).
type ChainCheckpoint struct {
	ID        int64
	EntryID   int64
	EntryHash []byte
	CreatedAt time.Time
}

// EntryHashHex retorna el hash de la entrada en hex (para logs y APIs).
func (c ChainCheckpoint) EntryHashHex() string { return hex.EncodeToString(c.EntryHash) }

// CheckpointMismatch describe un checkpoint que la cadena actual ya no respeta.
type CheckpointMismatch struct {
	CheckpointID int64
	EntryID      int64
	EntryMissing bool // la entrada ya no existe (cola truncada)
}
