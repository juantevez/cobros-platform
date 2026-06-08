package http

import "github.com/gin-gonic/gin"

// RegisterRoutes registra las rutas del contexto Ledger en un grupo de Gin existente.
// Se llama desde cmd/api/main.go pasando el grupo protegido por JWT.
func RegisterRoutes(rg *gin.RouterGroup, accounts *AccountHandler, entries *EntryHandler) {
	ledger := rg.Group("/ledger")
	{
		// Cuentas contables
		ledger.POST("/accounts", accounts.Create)
		ledger.GET("/accounts/:accountID/balance", accounts.GetBalance)

		// Asientos
		ledger.POST("/entries", entries.Post)
		ledger.POST("/entries/:entryID/reverse", entries.Reverse)
	}
}
