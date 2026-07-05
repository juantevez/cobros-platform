package application

import (
	"context"
	"time"

	"github.com/juantevez/cobros-platform/context/reporting/domain"
)

// GetReportsUseCase resuelve las consultas del dashboard sobre el read-model.
type GetReportsUseCase struct {
	reader ReportReader
}

func NewGetReportsUseCase(reader ReportReader) *GetReportsUseCase {
	return &GetReportsUseCase{reader: reader}
}

// GetVolume retorna la serie de volumen transaccional agregada por bucket.
func (uc *GetReportsUseCase) GetVolume(ctx context.Context, q VolumeQuery) ([]domain.VolumePoint, error) {
	if err := validateRange(q.From, q.To); err != nil {
		return nil, err
	}
	return uc.reader.Volume(ctx, q)
}

// GetRevenue retorna el resumen de comisiones cobradas por período.
func (uc *GetReportsUseCase) GetRevenue(ctx context.Context, q RevenueQuery) ([]domain.RevenueSummary, error) {
	if err := validateRange(q.From, q.To); err != nil {
		return nil, err
	}
	return uc.reader.Revenue(ctx, q)
}

// GetBalances retorna el saldo neto por tipo de cuenta del comercio.
func (uc *GetReportsUseCase) GetBalances(ctx context.Context, tenantID string) ([]domain.TenantBalance, error) {
	return uc.reader.Balances(ctx, tenantID)
}

// validateRange verifica que el rango sea coherente. Si ambos son cero, se
// interpreta como "sin filtro" y se delega al reader.
func validateRange(from, to time.Time) error {
	if from.IsZero() || to.IsZero() {
		return nil
	}
	if from.After(to) {
		return domain.ErrInvalidRange
	}
	return nil
}
