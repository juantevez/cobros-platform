package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// pgStore implementa Store sobre PostgreSQL.
type pgStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore crea un Store respaldado por PostgreSQL.
func NewPostgresStore(pool *pgxpool.Pool) Store {
	return &pgStore{pool: pool}
}

// Save persiste el mensaje en la tabla outbox_messages.
// DEBE ejecutarse dentro de la transacción activa del contexto (ConnFromContext
// detecta la tx inyectada por el TxManager).
func (s *pgStore) Save(ctx context.Context, msg Message) error {
	conn := postgres.ConnFromContext(ctx, s.pool)

	headers, err := json.Marshal(msg.Headers)
	if err != nil {
		return fmt.Errorf("outbox: marshal headers: %w", err)
	}

	_, err = conn.Exec(ctx, `
		INSERT INTO outbox_messages
			(id, tenant_id, subject, payload, headers, created_at)
		VALUES
			($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO NOTHING`,
		msg.ID,
		nullStr(msg.TenantID),
		msg.Subject,
		msg.Payload,
		headers,
		msg.CreatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("outbox: save %q: %w", msg.Subject, err)
	}
	return nil
}

// FetchPending retorna hasta n mensajes no publicados, en orden FIFO.
// Usa FOR UPDATE SKIP LOCKED para que múltiples instancias del relay
// no procesen el mismo mensaje simultáneamente.
func (s *pgStore) FetchPending(ctx context.Context, n int) ([]Message, error) {
	// FetchPending corre fuera de transacciones de negocio (no hay tenant activo),
	// por eso usa s.pool directamente.
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, subject, payload, headers, created_at
		FROM outbox_messages
		WHERE published_at IS NULL
		ORDER BY created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`,
		n,
	)
	if err != nil {
		return nil, fmt.Errorf("outbox: fetch pending: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		var tenantID *string
		var headersJSON []byte

		if err := rows.Scan(
			&m.ID, &tenantID, &m.Subject, &m.Payload, &headersJSON, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("outbox: scan row: %w", err)
		}

		if tenantID != nil {
			m.TenantID = *tenantID
		}
		if len(headersJSON) > 0 {
			if err := json.Unmarshal(headersJSON, &m.Headers); err != nil {
				return nil, fmt.Errorf("outbox: unmarshal headers: %w", err)
			}
		}
		msgs = append(msgs, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox: iterate rows: %w", err)
	}

	return msgs, nil
}

// MarkPublished registra que el mensaje fue publicado exitosamente.
func (s *pgStore) MarkPublished(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE outbox_messages
		SET published_at = $1
		WHERE id = $2`,
		time.Now().UTC(),
		id,
	)
	if err != nil {
		return fmt.Errorf("outbox: mark published %q: %w", id, err)
	}
	return nil
}

// nullStr convierte un string vacío en nil para columnas nullable.
func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
