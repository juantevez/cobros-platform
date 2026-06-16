package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

type pgEndpointRepository struct {
	pool *pgxpool.Pool
}

func NewEndpointRepository(pool *pgxpool.Pool) *pgEndpointRepository {
	return &pgEndpointRepository{pool: pool}
}

func (r *pgEndpointRepository) Save(ctx context.Context, e *domain.WebhookEndpoint) error {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		INSERT INTO webhook_endpoints
			(id, tenant_id, url, secret, secret_hint, events, active, description, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		e.ID().String(), e.TenantID().String(),
		e.URL(), e.Secret(), e.SecretHint(),
		toTextArray(e.Events()),
		e.Active(), e.Description(),
		e.CreatedAt(), e.UpdatedAt(),
	)
	if err != nil {
		return fmt.Errorf("endpoint repo: save: %w", err)
	}
	return nil
}

func (r *pgEndpointRepository) Update(ctx context.Context, e *domain.WebhookEndpoint) error {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		UPDATE webhook_endpoints SET active=$2, updated_at=$3 WHERE id=$1`,
		e.ID().String(), e.Active(), e.UpdatedAt(),
	)
	if err != nil {
		return fmt.Errorf("endpoint repo: update: %w", err)
	}
	return nil
}

func (r *pgEndpointRepository) FindByID(ctx context.Context, id domain.EndpointID) (*domain.WebhookEndpoint, error) {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, baseEndpointSelect+" WHERE id=$1", id.String())
	return scanEndpoint(row)
}

func (r *pgEndpointRepository) FindByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.WebhookEndpoint, error) {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	rows, err := conn.Query(ctx,
		baseEndpointSelect+" WHERE tenant_id=$1 ORDER BY created_at DESC",
		tenantID.String())
	if err != nil {
		return nil, fmt.Errorf("endpoint repo: list: %w", err)
	}
	defer rows.Close()
	return scanEndpoints(rows)
}

func (r *pgEndpointRepository) FindActiveByTenantAndEvent(
	ctx context.Context,
	tenantID domain.TenantID,
	eventType string,
) ([]*domain.WebhookEndpoint, error) {
	// Busca endpoints suscritos al event_type con o sin sufijo de versión.
	// events @> ARRAY[$2] verifica que el array contenga ese elemento.
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	stripped := stripVersion(eventType)
	rows, err := conn.Query(ctx,
		baseEndpointSelect+`
		WHERE tenant_id=$1 AND active=true
		  AND (events @> ARRAY[$2::text] OR events @> ARRAY[$3::text])`,
		tenantID.String(), eventType, stripped,
	)
	if err != nil {
		return nil, fmt.Errorf("endpoint repo: find active by event: %w", err)
	}
	defer rows.Close()
	return scanEndpoints(rows)
}

const baseEndpointSelect = `
	SELECT id, tenant_id, url, secret, secret_hint, events,
	       active, description, created_at, updated_at
	FROM webhook_endpoints`

func scanEndpoint(row pgx.Row) (*domain.WebhookEndpoint, error) {
	var (
		idStr, tenantIDStr, url, secret, hint, desc string
		active                                      bool
		createdAt, updatedAt                        time.Time
		eventsArr                                   pgtype.Array[pgtype.Text]
	)
	if err := row.Scan(&idStr, &tenantIDStr, &url, &secret, &hint,
		&eventsArr, &active, &desc, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrEndpointNotFound
		}
		return nil, fmt.Errorf("endpoint repo: scan: %w", err)
	}

	events := make([]string, 0, len(eventsArr.Elements))
	for _, el := range eventsArr.Elements {
		if el.Valid {
			events = append(events, el.String)
		}
	}

	return domain.ReconstituteWebhookEndpoint(
		domain.EndpointID(idStr), domain.TenantID(tenantIDStr),
		url, secret, hint, desc, events, active,
		createdAt.UTC(), updatedAt.UTC(),
	), nil
}

func scanEndpoints(rows pgx.Rows) ([]*domain.WebhookEndpoint, error) {
	var result []*domain.WebhookEndpoint
	for rows.Next() {
		e, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

func wrapErr(op string, err error) error {
	if err != nil {
		return fmt.Errorf("endpoint repo: %s: %w", op, err)
	}
	return nil
}

// toTextArray convierte []string a pgtype.Array[pgtype.Text] para pgx v5.
func toTextArray(ss []string) pgtype.Array[pgtype.Text] {
	elements := make([]pgtype.Text, len(ss))
	for i, s := range ss {
		elements[i] = pgtype.Text{String: s, Valid: true}
	}
	return pgtype.Array[pgtype.Text]{
		Elements: elements,
		Dims:     []pgtype.ArrayDimension{{Length: int32(len(ss)), LowerBound: 1}},
		Valid:    true,
	}
}

// stripVersion quita el sufijo de versión: "payment.captured.v1" → "payment.captured"
func stripVersion(s string) string {
	parts := strings.Split(s, ".")
	if len(parts) > 1 {
		last := parts[len(parts)-1]
		if len(last) > 1 && last[0] == 'v' {
			allDigits := true
			for _, c := range last[1:] {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				return strings.Join(parts[:len(parts)-1], ".")
			}
		}
	}
	return s
}
