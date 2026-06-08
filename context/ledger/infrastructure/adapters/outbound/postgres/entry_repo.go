package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

type pgEntryRepository struct {
	pool *pgxpool.Pool
}

func NewEntryRepository(pool *pgxpool.Pool) *pgEntryRepository {
	return &pgEntryRepository{pool: pool}
}

// Save persiste el JournalEntry y todos sus Postings en la misma transacción.
func (r *pgEntryRepository) Save(ctx context.Context, e *domain.JournalEntry) error {
	conn := postgres.ConnFromContext(ctx, r.pool)

	metadata, err := json.Marshal(e.Metadata())
	if err != nil {
		return fmt.Errorf("entry repo: marshal metadata: %w", err)
	}

	_, err = conn.Exec(ctx, `
		INSERT INTO journal_entries
			(id, tenant_id, idempotency_key, description, metadata, occurred_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.ID().String(),
		e.TenantID().String(),
		e.IdempotencyKey(),
		e.Description(),
		metadata,
		e.OccurredAt(),
		e.CreatedAt(),
	)
	if err != nil {
		return fmt.Errorf("entry repo: save entry: %w", err)
	}

	// Guardar los postings del entry.
	for _, p := range e.Postings() {
		if _, err := conn.Exec(ctx, `
			INSERT INTO postings (id, entry_id, tenant_id, account_id, direction, amount, currency)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			p.ID().String(),
			e.ID().String(),
			e.TenantID().String(),
			p.AccountID().String(),
			p.Direction().String(),
			p.Money().Amount(),
			p.Money().Currency(),
		); err != nil {
			return fmt.Errorf("entry repo: save posting %s: %w", p.ID(), err)
		}
	}

	return nil
}

func (r *pgEntryRepository) FindByID(ctx context.Context, id domain.EntryID) (*domain.JournalEntry, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, `
		SELECT id, tenant_id, idempotency_key, description, metadata, occurred_at, created_at
		FROM journal_entries WHERE id = $1`,
		id.String(),
	)
	return r.scanEntryWithPostings(ctx, conn, row)
}

func (r *pgEntryRepository) FindByIdempotencyKey(
	ctx context.Context,
	tenantID domain.TenantID,
	key string,
) (*domain.JournalEntry, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, `
		SELECT id, tenant_id, idempotency_key, description, metadata, occurred_at, created_at
		FROM journal_entries
		WHERE tenant_id = $1 AND idempotency_key = $2`,
		tenantID.String(), key,
	)
	return r.scanEntryWithPostings(ctx, conn, row)
}

// scanEntryWithPostings carga el entry y luego sus postings.
func (r *pgEntryRepository) scanEntryWithPostings(
	ctx context.Context,
	conn postgres.Conn,
	row pgx.Row,
) (*domain.JournalEntry, error) {
	var (
		idStr, tenantIDStr, idempotencyKey, description string
		metadataJSON                                     []byte
		occurredAt, createdAt                            time.Time
	)

	if err := row.Scan(
		&idStr, &tenantIDStr, &idempotencyKey, &description,
		&metadataJSON, &occurredAt, &createdAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrEntryNotFound
		}
		return nil, fmt.Errorf("entry repo: scan entry: %w", err)
	}

	var metadata map[string]string
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
			return nil, fmt.Errorf("entry repo: unmarshal metadata: %w", err)
		}
	}

	// Cargar los postings del entry.
	rows, err := conn.Query(ctx, `
		SELECT id, account_id, direction, amount, currency
		FROM postings WHERE entry_id = $1 ORDER BY id`,
		idStr,
	)
	if err != nil {
		return nil, fmt.Errorf("entry repo: query postings: %w", err)
	}
	defer rows.Close()

	var postings []domain.Posting
	for rows.Next() {
		var postIDStr, accountIDStr, dirStr, currency string
		var amount int64
		if err := rows.Scan(&postIDStr, &accountIDStr, &dirStr, &amount, &currency); err != nil {
			return nil, fmt.Errorf("entry repo: scan posting: %w", err)
		}
		dir, _ := domain.ParseDirection(dirStr)
		money, _ := domain.NewMoney(amount, currency)
		postings = append(postings, domain.ReconstitutePosting(
			domain.PostingID(postIDStr),
			domain.AccountID(accountIDStr),
			dir,
			money,
		))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entry repo: iterate postings: %w", err)
	}

	return domain.ReconstituteJournalEntry(
		domain.EntryID(idStr),
		domain.TenantID(tenantIDStr),
		idempotencyKey,
		description,
		metadata,
		postings,
		occurredAt.UTC(),
		createdAt.UTC(),
	), nil
}
