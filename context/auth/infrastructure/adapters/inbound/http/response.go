package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// ErrorResponse es el cuerpo JSON de toda respuesta de error.
type ErrorResponse struct {
	Error string `json:"error"`
}

// respondJSON escribe una respuesta JSON con el status dado.
func respondJSON(c *gin.Context, status int, body any) {
	c.JSON(status, body)
}

// respondError escribe una respuesta de error con mensaje fijo.
func respondError(c *gin.Context, status int, msg string) {
	c.JSON(status, ErrorResponse{Error: msg})
}

// respondDomainError mapea errores del dominio a códigos HTTP apropiados.
// Los errores internos nunca exponen detalles al cliente.
func respondDomainError(c *gin.Context, err error) {
	switch {
	// 400 Bad Request
	case errors.Is(err, domain.ErrInvalidEmail),
		errors.Is(err, domain.ErrInvalidRole),
		errors.Is(err, domain.ErrInvalidEnvironment),
		errors.Is(err, domain.ErrInvalidID),
		errors.Is(err, domain.ErrEmptyLegalName),
		errors.Is(err, domain.ErrEmptyPassword):
		respondError(c, http.StatusBadRequest, err.Error())

	// 401 Unauthorized
	case errors.Is(err, domain.ErrInvalidCredentials),
		errors.Is(err, domain.ErrApiKeyRevoked):
		respondError(c, http.StatusUnauthorized, err.Error())

	// 403 Forbidden
	case errors.Is(err, domain.ErrTenantSuspended),
		errors.Is(err, domain.ErrUserSuspended),
		errors.Is(err, domain.ErrTenantNotActive):
		respondError(c, http.StatusForbidden, err.Error())

	// 404 Not Found
	case errors.Is(err, domain.ErrTenantNotFound),
		errors.Is(err, domain.ErrUserNotFound),
		errors.Is(err, domain.ErrApiKeyNotFound),
		errors.Is(err, domain.ErrMembershipNotFound):
		respondError(c, http.StatusNotFound, err.Error())

	// 409 Conflict
	case errors.Is(err, domain.ErrEmailAlreadyExists),
		errors.Is(err, domain.ErrApiKeyAlreadyRevoked):
		respondError(c, http.StatusConflict, err.Error())

	// 422 Unprocessable Entity (transiciones de estado inválidas)
	case errors.Is(err, domain.ErrTenantCannotTransition):
		respondError(c, http.StatusUnprocessableEntity, err.Error())

	// 500 Internal Server Error (todo lo demás)
	default:
		// No exponer detalles del error interno al cliente.
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal server error"})
	}
}
