package http

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

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
		TenantPlanID string `json:"TenantPlanID"`
		PlanName     string `json:"PlanName"`
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
