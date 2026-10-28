package tenant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/juantevez/cobros-platform/context/payment/domain"
)

const statusSQL = `SELECT t.status,\s+EXISTS \(SELECT 1 FROM tenant_compliance_holds h\s+WHERE h.tenant_id = t.id AND h.released_at IS NULL\)\s+FROM tenants t WHERE t.id = \$1`

var gateCols = []string{"status", "on_hold"}

func newMock(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("new mock pool: %v", err)
	}
	t.Cleanup(mock.Close)
	return mock
}

func TestStatusGate_EnsureCanProcessPayments(t *testing.T) {
	tenantID := domain.TenantID(uuid.NewString())

	t.Run("active tenant can process payments", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery(statusSQL).WithArgs(tenantID.String()).
			WillReturnRows(pgxmock.NewRows(gateCols).AddRow("active", false))

		if err := NewStatusGate(mock).EnsureCanProcessPayments(context.Background(), tenantID); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})

	// pending = KYC sin aprobar; suspended = bloqueado por el operador.
	for _, status := range []string{"pending", "suspended"} {
		t.Run(status+" tenant is rejected", func(t *testing.T) {
			mock := newMock(t)
			mock.ExpectQuery(statusSQL).WithArgs(tenantID.String()).
				WillReturnRows(pgxmock.NewRows(gateCols).AddRow(status, false))

			err := NewStatusGate(mock).EnsureCanProcessPayments(context.Background(), tenantID)
			if !errors.Is(err, domain.ErrTenantNotActive) || !strings.Contains(err.Error(), status) {
				t.Fatalf("expected ErrTenantNotActive mentioning %q, got %v", status, err)
			}
		})
	}

	t.Run("active tenant on compliance hold is rejected with the generic error", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery(statusSQL).WithArgs(tenantID.String()).
			WillReturnRows(pgxmock.NewRows(gateCols).AddRow("active", true))

		err := NewStatusGate(mock).EnsureCanProcessPayments(context.Background(), tenantID)
		if !errors.Is(err, domain.ErrTenantNotActive) {
			t.Fatalf("expected ErrTenantNotActive, got %v", err)
		}
		if strings.Contains(strings.ToLower(err.Error()), "sanction") || strings.Contains(strings.ToLower(err.Error()), "compliance") {
			t.Errorf("error must not reveal the hold reason: %v", err)
		}
	})

	t.Run("unknown tenant is rejected", func(t *testing.T) {
		mock := newMock(t)
		mock.ExpectQuery(statusSQL).WithArgs(tenantID.String()).WillReturnError(pgx.ErrNoRows)

		err := NewStatusGate(mock).EnsureCanProcessPayments(context.Background(), tenantID)
		if !errors.Is(err, domain.ErrTenantNotActive) {
			t.Fatalf("expected ErrTenantNotActive, got %v", err)
		}
	})

	t.Run("db error is wrapped and is not reported as inactive", func(t *testing.T) {
		boom := errors.New("db down")
		mock := newMock(t)
		mock.ExpectQuery(statusSQL).WithArgs(tenantID.String()).WillReturnError(boom)

		err := NewStatusGate(mock).EnsureCanProcessPayments(context.Background(), tenantID)
		if !errors.Is(err, boom) || errors.Is(err, domain.ErrTenantNotActive) {
			t.Fatalf("expected wrapped db error, got %v", err)
		}
	})
}
