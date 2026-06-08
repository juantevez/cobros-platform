package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/auth/application"
)

// UserHandler gestiona registro de usuarios y asignación de roles.
type UserHandler struct {
	registerUser *application.RegisterUserUseCase
	assignRole   *application.AssignRoleUseCase
}

func NewUserHandler(
	registerUser *application.RegisterUserUseCase,
	assignRole *application.AssignRoleUseCase,
) *UserHandler {
	return &UserHandler{
		registerUser: registerUser,
		assignRole:   assignRole,
	}
}

// ── Register ──────────────────────────────────────────────────────────────────

type registerUserRequest struct {
	Email    string `json:"email"    binding:"required"`
	Password string `json:"password" binding:"required"`
	Role     string `json:"role"     binding:"required"`
}

type registerUserResponse struct {
	UserID string `json:"user_id"`
}

// Register crea un usuario en el tenant del caller.
//
//	POST /api/v1/tenants/:tenantID/users
func (h *UserHandler) Register(c *gin.Context) {
	claims, ok := ClaimsFromContext(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	var req registerUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	// El tenantID del path debe coincidir con el del token (aislamiento).
	if c.Param("tenantID") != claims.TenantID.String() {
		respondError(c, http.StatusForbidden, "cannot create users in another tenant")
		return
	}

	result, err := h.registerUser.Execute(c.Request.Context(), application.RegisterUserCmd{
		TenantID: claims.TenantID.String(),
		Email:    req.Email,
		Password: req.Password,
		Role:     req.Role,
	})
	if err != nil {
		respondDomainError(c, err)
		return
	}

	respondJSON(c, http.StatusCreated, registerUserResponse{UserID: result.UserID})
}

// ── AssignRole ────────────────────────────────────────────────────────────────

type assignRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

// AssignRole asigna o modifica el rol de un usuario en el tenant.
//
//	PUT /api/v1/tenants/:tenantID/members/:userID/role
func (h *UserHandler) AssignRole(c *gin.Context) {
	claims, ok := ClaimsFromContext(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "authentication required")
		return
	}

	if c.Param("tenantID") != claims.TenantID.String() {
		respondError(c, http.StatusForbidden, "cannot manage users in another tenant")
		return
	}

	var req assignRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.assignRole.Execute(c.Request.Context(), application.AssignRoleCmd{
		TenantID:   claims.TenantID.String(),
		UserID:     c.Param("userID"),
		Role:       req.Role,
		AssignedBy: claims.UserID.String(),
	}); err != nil {
		respondDomainError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
