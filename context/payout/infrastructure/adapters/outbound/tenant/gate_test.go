package tenant

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/payout/domain"
)

const gateSQL = `SELECT t.status,\s+EXISTS \(SELECT 1 FROM tenant_compliance_holds h\s+WHERE h.tenant_id = t.id AND h.released_at IS NULL\)\s+FROM tenants t WHERE t.id = \$1`

var gateCols = []string{"status", "on_hold"}

func TestPayoutGate_EnsurePayoutsAllowed(t *testing.T) {
	tenantID := domain.TenantID(uuid.NewString())

	run := func(t *testing.T, setup func(pgxmock.PgxPoolIface)) error {
		t.Helper()
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatalf("mock: %v", err)
		}
		defer mock.Close()
		setup(mock)
		return NewPayoutGate(mock).EnsurePayoutsAllowed(context.Background(), tenantID)
	}
	rows := func(status string, hold bool) func(pgxmock.PgxPoolIface) {
		return func(m pgxmock.PgxPoolIface) {
			m.ExpectQuery(gateSQL).WithArgs(tenantID.String()).
				WillReturnRows(pgxmock.NewRows(gateCols).AddRow(status, hold))
		}
	}

	if err := run(t, rows("active", false)); err != nil {
		t.Fatalf("active tenant without hold: unexpected error %v", err)
	}
	for name, setup := range map[string]func(pgxmock.PgxPoolIface){
		"pending":   rows("pending", false),
		"suspended": rows("suspended", false),
		"on hold":   rows("active", true),
		"not found": func(m pgxmock.PgxPoolIface) {
			m.ExpectQuery(gateSQL).WithArgs(tenantID.String()).WillReturnError(pgx.ErrNoRows)
		},
	} {
		if err := run(t, setup); !errors.Is(err, domain.ErrPayoutsNotAllowed) {
			t.Errorf("%s: expected ErrPayoutsNotAllowed, got %v", name, err)
		}
	}

	boom := errors.New("db down")
	err := run(t, func(m pgxmock.PgxPoolIface) {
		m.ExpectQuery(gateSQL).WithArgs(tenantID.String()).WillReturnError(boom)
	})
	if !errors.Is(err, boom) || errors.Is(err, domain.ErrPayoutsNotAllowed) {
		t.Fatalf("db error should be wrapped, not reported as not allowed: %v", err)
	}
}
