package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

type ErrorResponse struct {
	Error string `json:"error"`
}

func respondJSON(c *gin.Context, status int, body any) { c.JSON(status, body) }

func respondError(c *gin.Context, status int, msg string) {
	c.JSON(status, ErrorResponse{Error: msg})
}

func respondDomainError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrAccountNotFound),
		errors.Is(err, domain.ErrEntryNotFound):
		respondError(c, http.StatusNotFound, err.Error())

	case errors.Is(err, domain.ErrEntryNotBalanced),
		errors.Is(err, domain.ErrCurrencyMismatch),
		errors.Is(err, domain.ErrNotEnoughPostings),
		errors.Is(err, domain.ErrZeroAmount),
		errors.Is(err, domain.ErrInvalidDirection),
		errors.Is(err, domain.ErrInvalidAccountType),
		errors.Is(err, domain.ErrInvalidCurrency),
		errors.Is(err, domain.ErrNegativeAmount):
		respondError(c, http.StatusUnprocessableEntity, err.Error())

	case errors.Is(err, domain.ErrEntryAlreadyExists):
		respondError(c, http.StatusConflict, err.Error())

	default:
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal server error"})
	}
}
