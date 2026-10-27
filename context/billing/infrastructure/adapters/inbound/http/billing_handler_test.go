package http

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	authdomain "github.com/juantevez/cobros-platform/context/auth/domain"
	"github.com/juantevez/cobros-platform/context/billing/domain"
)

// ── CreatePlan ────────────────────────────────────────────────────────────────

func TestCreatePlan_Success(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/v1/billing/plans", map[string]any{
		"name":          "Plan Pro",
		"base_rate_bps": 250,
		"currency":      "ARS",
		"method_rates": []map[string]any{
			{"method": "card", "rate_bps": 300, "fixed_amount": 60},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		PlanID string `json:"plan_id"`
	}
	decodeBody(t, rec, &body)
	if body.PlanID == "" {
		t.Error("expected a plan id")
	}
	if len(env.pub.published) != 1 {
		t.Errorf("expected 1 event, got %d", len(env.pub.published))
	}
}

func TestCreatePlan_BadBody(t *testing.T) {
	env := newTestEnv(t)
	// falta currency (binding required)
	rec := env.do(http.MethodPost, "/api/v1/billing/plans", map[string]any{
		"name": "Plan", "base_rate_bps": 250,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCreatePlan_InvalidMethodMapsTo400(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/v1/billing/plans", map[string]any{
		"name": "Plan", "base_rate_bps": 250, "currency": "ARS",
		"method_rates": []map[string]any{{"method": "crypto", "rate_bps": 100}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (invalid payment method), body=%s", rec.Code, rec.Body.String())
	}
}

// Zero rate es un plan válido (comisión 0%); no debe rechazarse por binding.
func TestCreatePlan_ZeroRateAccepted(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/v1/billing/plans", map[string]any{
		"name": "Plan Gratis", "base_rate_bps": 0, "currency": "ARS",
		"method_rates": []map[string]any{{"method": "card", "rate_bps": 0, "fixed_amount": 0}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (zero rate is valid), body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreatePlan_InvalidCurrencyMapsTo400(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/v1/billing/plans", map[string]any{
		"name": "Plan", "base_rate_bps": 250, "currency": "PESOS",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (invalid currency), body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreatePlan_NegativeFixedAmountMapsTo400(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/v1/billing/plans", map[string]any{
		"name": "Plan", "base_rate_bps": 250, "currency": "ARS",
		"base_fixed_amount": -1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (negative fixed amount), body=%s", rec.Code, rec.Body.String())
	}
}

// ── ListPlans ─────────────────────────────────────────────────────────────────

func TestListPlans_Success(t *testing.T) {
	env := newTestEnv(t)
	env.seedPlan(t, "Plan A", 250)
	env.seedPlan(t, "Plan B", 300)

	rec := env.do(http.MethodGet, "/api/v1/billing/plans", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Plans []map[string]any `json:"plans"`
		Count int              `json:"count"`
	}
	decodeBody(t, rec, &body)
	if body.Count != 2 || len(body.Plans) != 2 {
		t.Errorf("expected 2 plans, got count=%d len=%d", body.Count, len(body.Plans))
	}
}

// ── GetPlan ───────────────────────────────────────────────────────────────────

func TestGetPlan_Success(t *testing.T) {
	env := newTestEnv(t)
	p := env.seedPlan(t, "Plan Base", 250)

	rec := env.do(http.MethodGet, "/api/v1/billing/plans/"+p.ID().String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var view struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	decodeBody(t, rec, &view)
	if view.ID != p.ID().String() || view.Name != "Plan Base" {
		t.Errorf("unexpected view: %+v", view)
	}
}

func TestGetPlan_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/v1/billing/plans/"+domain.NewPlanID().String(), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGetPlan_InvalidID(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/v1/billing/plans/not-a-uuid", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (unmapped parse error), body=%s", rec.Code, rec.Body.String())
	}
}

// ── AssignPlan ────────────────────────────────────────────────────────────────

func TestAssignPlan_Success(t *testing.T) {
	env := newTestEnv(t)
	p := env.seedPlan(t, "Plan Base", 250)

	tenantID := uuid.NewString()
	rec := env.do(http.MethodPost, "/api/v1/billing/tenants/"+tenantID+"/plan", map[string]any{
		"plan_id": p.ID().String(),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		TenantPlanID string `json:"tenant_plan_id"`
		PlanName     string `json:"plan_name"`
	}
	decodeBody(t, rec, &body)
	if body.TenantPlanID == "" || body.PlanName != "Plan Base" {
		t.Errorf("unexpected response: %+v", body)
	}
}

func TestAssignPlan_WithOverrides(t *testing.T) {
	env := newTestEnv(t)
	p := env.seedPlan(t, "Plan Base", 250)

	tenantID := uuid.NewString()
	rate := int64(100)
	fixed := int64(25)
	rec := env.do(http.MethodPost, "/api/v1/billing/tenants/"+tenantID+"/plan", map[string]any{
		"plan_id":             p.ID().String(),
		"custom_rate_bps":     rate,
		"custom_fixed_amount": fixed,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	tid, _ := domain.ParseTenantID(tenantID)
	tp, err := env.tenantPlans.FindActive(context.Background(), tid)
	if err != nil {
		t.Fatalf("expected active tenant plan: %v", err)
	}
	if tp.CustomRateBps() == nil || *tp.CustomRateBps() != 100 {
		t.Errorf("custom rate not applied: %v", tp.CustomRateBps())
	}
}

func TestAssignPlan_BadBody(t *testing.T) {
	env := newTestEnv(t)
	// falta plan_id (binding required)
	rec := env.do(http.MethodPost, "/api/v1/billing/tenants/"+uuid.NewString()+"/plan", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAssignPlan_PlanNotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/v1/billing/tenants/"+uuid.NewString()+"/plan", map[string]any{
		"plan_id": domain.NewPlanID().String(),
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAssignPlan_InactivePlanConflict(t *testing.T) {
	env := newTestEnv(t)
	p := env.seedPlan(t, "Plan Base", 250)
	p.Deactivate()

	rec := env.do(http.MethodPost, "/api/v1/billing/tenants/"+uuid.NewString()+"/plan", map[string]any{
		"plan_id": p.ID().String(),
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (plan inactive), body=%s", rec.Code, rec.Body.String())
	}
}

// ── Autorización (RouteGuards) ───────────────────────────────────────────────

func TestRegisterRoutes_PanicsWithoutMandatoryGuards(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when mandatory guards are missing")
		}
	}()
	r := gin.New()
	RegisterRoutes(r.Group("/api/v1"), &BillingHandler{}, RouteGuards{})
}

func TestAuthorization_PlatformSupportCanManagePlans(t *testing.T) {
	env := newTestEnvWithGuards(t, RouteGuards{
		ManagePlans: allowRole(authdomain.RolePlatformSupport),
		AssignPlan:  allowRole(authdomain.RolePlatformSupport),
		ReadMyPlan:  allowRole(authdomain.RoleAdmin, authdomain.RolePlatformSupport),
	}, authdomain.RolePlatformSupport)

	rec := env.do(http.MethodPost, "/api/v1/billing/plans", map[string]any{
		"name": "Plan Pro", "base_rate_bps": 250, "currency": "ARS",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 for platform_support, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuthorization_TenantAdminCannotCreatePlans(t *testing.T) {
	env := newTestEnvWithGuards(t, RouteGuards{
		ManagePlans: allowRole(authdomain.RolePlatformSupport),
		AssignPlan:  allowRole(authdomain.RolePlatformSupport),
		ReadMyPlan:  allowRole(authdomain.RoleAdmin, authdomain.RolePlatformSupport),
	}, authdomain.RoleAdmin)

	rec := env.do(http.MethodPost, "/api/v1/billing/plans", map[string]any{
		"name": "Plan Pro", "base_rate_bps": 250, "currency": "ARS",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for tenant admin creating plans, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuthorization_TenantAdminCannotAssignPlans(t *testing.T) {
	env := newTestEnvWithGuards(t, RouteGuards{
		ManagePlans: allowRole(authdomain.RolePlatformSupport),
		AssignPlan:  allowRole(authdomain.RolePlatformSupport),
		ReadMyPlan:  allowRole(authdomain.RoleAdmin, authdomain.RolePlatformSupport),
	}, authdomain.RoleAdmin)
	p := env.seedPlan(t, "Plan Base", 250)

	rec := env.do(http.MethodPost, "/api/v1/billing/tenants/"+uuid.NewString()+"/plan", map[string]any{
		"plan_id": p.ID().String(),
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for tenant admin assigning plans, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuthorization_TenantAdminCanReadMyPlan(t *testing.T) {
	env := newTestEnvWithGuards(t, RouteGuards{
		ManagePlans: allowRole(authdomain.RolePlatformSupport),
		AssignPlan:  allowRole(authdomain.RolePlatformSupport),
		ReadMyPlan:  allowRole(authdomain.RoleAdmin, authdomain.RolePlatformSupport),
	}, authdomain.RoleAdmin)
	p := env.seedPlan(t, "Plan Base", 250)

	tp, err := domain.NewTenantPlan(domain.NewTenantPlanID(), env.tenantID, p, -1, -1, timeNow())
	if err != nil {
		t.Fatalf("build tenant plan: %v", err)
	}
	tp.PullEvents()
	env.tenantPlans.active[env.tenantID] = tp

	rec := env.do(http.MethodGet, "/api/v1/billing/my-plan", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for tenant admin reading own plan, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuthorization_ReadonlyCannotReadMyPlan(t *testing.T) {
	env := newTestEnvWithGuards(t, RouteGuards{
		ManagePlans: allowRole(authdomain.RolePlatformSupport),
		AssignPlan:  allowRole(authdomain.RolePlatformSupport),
		ReadMyPlan:  allowRole(authdomain.RoleAdmin, authdomain.RolePlatformSupport),
	}, authdomain.RoleReadOnly)

	rec := env.do(http.MethodGet, "/api/v1/billing/my-plan", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for read_only, body=%s", rec.Code, rec.Body.String())
	}
}

// ── GetMyPlan ─────────────────────────────────────────────────────────────────

func TestGetMyPlan_Success(t *testing.T) {
	env := newTestEnv(t)
	p := env.seedPlan(t, "Plan Base", 250)

	// Asignar el plan al tenant del contexto.
	tp, err := domain.NewTenantPlan(domain.NewTenantPlanID(), env.tenantID, p, -1, -1, timeNow())
	if err != nil {
		t.Fatalf("build tenant plan: %v", err)
	}
	tp.PullEvents()
	env.tenantPlans.active[env.tenantID] = tp

	rec := env.do(http.MethodGet, "/api/v1/billing/my-plan", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var view struct {
		TenantID string `json:"tenant_id"`
		PlanName string `json:"plan_name"`
	}
	decodeBody(t, rec, &view)
	if view.TenantID != env.tenantID.String() || view.PlanName != "Plan Base" {
		t.Errorf("unexpected view: %+v", view)
	}
}

func TestGetMyPlan_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/v1/billing/my-plan", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
