package http

import "github.com/gin-gonic/gin"

// RegisterRoutes registra las rutas del contexto Ledger en un grupo de Gin existente.
// Se llama desde cmd/api/main.go pasando el grupo protegido por JWT.
//
// writeGuard autoriza las rutas que crean cuentas o mueven dinero. Se inyecta
// desde el composition root para no acoplar este contexto con Auth; en
// producción es RequireRole(platform_support). Los asientos del negocio los
// generan los consumers: si un comercio pudiera postear asientos acreditaría
// su propio merchant_balance y retiraría ese saldo vía payout.
func RegisterRoutes(rg *gin.RouterGroup, accounts *AccountHandler, entries *EntryHandler, writeGuard gin.HandlerFunc) {
	if writeGuard == nil {
		panic("ledger: RegisterRoutes requires a writeGuard")
	}

	ledger := rg.Group("/ledger")
	{
		// Cuentas contables
		ledger.POST("/accounts", writeGuard, accounts.Create)
		ledger.GET("/accounts/:accountID/balance", accounts.GetBalance)

		// Asientos
		ledger.POST("/entries", writeGuard, entries.Post)
		ledger.POST("/entries/:entryID/reverse", writeGuard, entries.Reverse)
	}
}
