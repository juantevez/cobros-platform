package http

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/ledger/application"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

type EntryHandler struct {
	postEntry    *application.PostEntryUseCase
	reverseEntry *application.ReverseEntryUseCase
}

func NewEntryHandler(
	postEntry *application.PostEntryUseCase,
	reverseEntry *application.ReverseEntryUseCase,
) *EntryHandler {
	return &EntryHandler{postEntry: postEntry, reverseEntry: reverseEntry}
}

// ── Post Entry ────────────────────────────────────────────────────────────────

type postingLineRequest struct {
	AccountID string `json:"account_id" binding:"required"`
	Direction string `json:"direction"  binding:"required"` // "debit" | "credit"
	Amount    int64  `json:"amount"     binding:"required,min=1"`
	Currency  string `json:"currency"   binding:"required"`
}

type postEntryRequest struct {
	IdempotencyKey string             `json:"idempotency_key" binding:"required"`
	Description    string             `json:"description"`
	OccurredAt     *time.Time         `json:"occurred_at"` // nil = now
	Metadata       map[string]string  `json:"metadata"`
	Lines          []postingLineRequest `json:"lines" binding:"required,min=2"`
}

type postEntryResponse struct {
	EntryID     string `json:"entry_id"`
	WasExisting bool   `json:"was_existing"`
}

// Post registra un asiento de doble partida.
//
//	POST /api/v1/ledger/entries
func (h *EntryHandler) Post(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	var req postEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	lines := make([]application.PostingLine, len(req.Lines))
	for i, l := range req.Lines {
		lines[i] = application.PostingLine{
			AccountID: l.AccountID,
			Direction: l.Direction,
			Amount:    l.Amount,
			Currency:  l.Currency,
		}
	}

	occurredAt := time.Now().UTC()
	if req.OccurredAt != nil {
		occurredAt = *req.OccurredAt
	}

	result, err := h.postEntry.Execute(c.Request.Context(), application.PostEntryCmd{
		TenantID:       tenantID,
		IdempotencyKey: req.IdempotencyKey,
		Description:    req.Description,
		OccurredAt:     occurredAt,
		Metadata:       req.Metadata,
		Lines:          lines,
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}

	status := http.StatusCreated
	if result.WasExisting {
		status = http.StatusOK // idempotente: ya existía
	}
	respondJSON(c, status, postEntryResponse{
		EntryID:     result.EntryID,
		WasExisting: result.WasExisting,
	})
}

// ── Reverse Entry ─────────────────────────────────────────────────────────────

type reverseEntryResponse struct {
	ReverseEntryID string `json:"reverse_entry_id"`
}

// Reverse crea un asiento que anula contablemente al entry indicado.
//
//	POST /api/v1/ledger/entries/:entryID/reverse
func (h *EntryHandler) Reverse(c *gin.Context) {
	tenantID, _ := postgres.TenantIDFromContext(c.Request.Context())

	result, err := h.reverseEntry.Execute(c.Request.Context(), application.ReverseEntryCmd{
		TenantID:        tenantID,
		OriginalEntryID: c.Param("entryID"),
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}

	respondJSON(c, http.StatusCreated, reverseEntryResponse{
		ReverseEntryID: result.ReverseEntryID,
	})
}
