package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/dispute/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

func validTenant() string { return uuid.NewString() }

func tenantID(t *testing.T) domain.TenantID {
	t.Helper()
	tid, err := domain.ParseTenantID(validTenant())
	if err != nil {
		t.Fatalf("tenant id: %v", err)
	}
	return tid
}

// ── OpenDispute ───────────────────────────────────────────────────────────────

func TestOpenDisputeUseCase_Execute(t *testing.T) {
	baseCmd := func() OpenDisputeCmd {
		return OpenDisputeCmd{
			PaymentID:    "pay-1",
			PSPReference: "psp-1",
			Amount:       5000,
			Currency:     "ARS",
			Reason:       "fraudulent",
		}
	}

	t.Run("opens dispute, saves and publishes", func(t *testing.T) {
		r := newRepo()
		p := &fakePublisher{}
		uc := NewOpenDisputeUseCase(r, fakePayments{}, fakeTx{}, p, newClock())

		res, err := uc.Execute(context.Background(), baseCmd())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.DisputeID == "" {
			t.Error("expected dispute id")
		}
		if len(r.saved) != 1 {
			t.Fatalf("expected 1 saved, got %d", len(r.saved))
		}
		if len(p.published) != 1 {
			t.Errorf("expected 1 published event, got %d", len(p.published))
		}
		if _, ok := p.published[0].(domain.DisputeOpenedEvent); !ok {
			t.Errorf("expected DisputeOpenedEvent, got %T", p.published[0])
		}
	})

	t.Run("default deadline applied when zero", func(t *testing.T) {
		r := newRepo()
		clock := newClock()
		uc := NewOpenDisputeUseCase(r, fakePayments{}, fakeTx{}, &fakePublisher{}, clock)
		if _, err := uc.Execute(context.Background(), baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		d := r.saved[0]
		if d.Deadline().IsZero() {
			t.Error("deadline should have been defaulted")
		}
		// El deadline por defecto se corre desde el reloj inyectado al caso de uso,
		// no desde el wall clock: así el test es determinístico y no depende de la
		// fecha en que se ejecuta (con un reloj fijo en el pasado, comparar contra
		// time.Now() real fallaba).
		want := clock.Now().Add(defaultResponseWindow)
		if !d.Deadline().Equal(want) {
			t.Errorf("default deadline = %v, want %v", d.Deadline(), want)
		}
	})

	t.Run("explicit deadline preserved", func(t *testing.T) {
		r := newRepo()
		uc := NewOpenDisputeUseCase(r, fakePayments{}, fakeTx{}, &fakePublisher{}, newClock())
		cmd := baseCmd()
		cmd.Deadline = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		if _, err := uc.Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !r.saved[0].Deadline().Equal(cmd.Deadline) {
			t.Errorf("deadline = %v, want %v", r.saved[0].Deadline(), cmd.Deadline)
		}
	})

	// Abrir es del operador: la disputa es del dueño del pago, no del tenant
	// del JWT, y se escribe con ese tenant (RLS). Antes el caso de uso partía
	// del tenant del caller y el dominio lo reasignaba en silencio.
	t.Run("dispute belongs to the payment owner and is written with its tenant", func(t *testing.T) {
		r := newRepo()
		p := &fakePublisher{}
		tx := &tenantTx{}
		owner := tenantID(t)
		payments := fakePayments{refs: map[string]domain.PaymentRef{"pay-1": capturedPayment("pay-1", owner)}}
		operatorCtx := pkgpostgres.WithTenantID(context.Background(), validTenant())

		if _, err := NewOpenDisputeUseCase(r, payments, tx, p, newClock()).Execute(operatorCtx, baseCmd()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.saved[0].TenantID() != owner {
			t.Errorf("dispute tenant = %s, want payment owner %s", r.saved[0].TenantID(), owner)
		}
		if ev := p.published[0].(domain.DisputeOpenedEvent); ev.TenantID != owner.String() || ev.EventTenantID() != owner.String() {
			t.Errorf("event tenant = %s / %s, want %s", ev.TenantID, ev.EventTenantID(), owner)
		}
		if tx.tenant != owner.String() {
			t.Errorf("tx tenant = %q, want payment owner %s", tx.tenant, owner)
		}
	})

	t.Run("payment without a valid tenant rejected", func(t *testing.T) {
		r := newRepo()
		ref := capturedPayment("pay-1", "")
		uc := NewOpenDisputeUseCase(r, fakePayments{refs: map[string]domain.PaymentRef{"pay-1": ref}}, fakeTx{}, &fakePublisher{}, newClock())
		if _, err := uc.Execute(context.Background(), baseCmd()); err == nil || len(r.saved) != 0 {
			t.Fatalf("expected error and nothing saved, got %v (saved %d)", err, len(r.saved))
		}
	})

	t.Run("invalid reason rejected", func(t *testing.T) {
		uc := NewOpenDisputeUseCase(newRepo(), fakePayments{}, fakeTx{}, &fakePublisher{}, newClock())
		cmd := baseCmd()
		cmd.Reason = "bogus"
		if _, err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrInvalidDisputeReason) {
			t.Fatalf("expected ErrInvalidDisputeReason, got %v", err)
		}
	})

	t.Run("duplicate dispute for payment rejected", func(t *testing.T) {
		r := newRepo()
		tid := tenantID(t)
		seedOpen(r, tid, time.Now().Add(24*time.Hour))
		uc := NewOpenDisputeUseCase(r, fakePayments{}, fakeTx{}, &fakePublisher{}, newClock())
		cmd := baseCmd()
		cmd.PaymentID = "pay-1" // ya existe
		if _, err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrDuplicateDispute) {
			t.Fatalf("expected ErrDuplicateDispute, got %v", err)
		}
	})

	t.Run("invalid domain (empty payment) rejected", func(t *testing.T) {
		uc := NewOpenDisputeUseCase(newRepo(), fakePayments{}, fakeTx{}, &fakePublisher{}, newClock())
		cmd := baseCmd()
		cmd.PaymentID = ""
		if _, err := uc.Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error for empty payment id")
		}
	})

	t.Run("save error propagated", func(t *testing.T) {
		r := newRepo()
		r.saveErr = errBoom
		uc := NewOpenDisputeUseCase(r, fakePayments{}, fakeTx{}, &fakePublisher{}, newClock())
		if _, err := uc.Execute(context.Background(), baseCmd()); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

// ── ContestDispute ────────────────────────────────────────────────────────────

func TestContestDisputeUseCase_Execute(t *testing.T) {
	future := testNow.Add(24 * time.Hour)
	evidence := []EvidenceInput{{EvidenceType: "receipt", Reference: "s3://r", Description: "d"}}

	t.Run("valid contest updates dispute", func(t *testing.T) {
		r := newRepo()
		tid := tenantID(t)
		d := seedOpen(r, tid, future)
		uc := NewContestDisputeUseCase(r, fakeTx{}, newClock())

		cmd := ContestDisputeCmd{TenantID: tid.String(), DisputeID: d.ID().String(), Evidence: evidence, Note: "prueba"}
		if err := uc.Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.updated) != 1 || r.updated[0].Status() != domain.StatusUnderReview {
			t.Errorf("dispute not updated to under_review: %+v", r.updated)
		}
		if len(r.updated[0].Evidence()) != 1 {
			t.Errorf("evidence not attached")
		}
	})

	t.Run("invalid tenant rejected", func(t *testing.T) {
		uc := NewContestDisputeUseCase(newRepo(), fakeTx{}, newClock())
		cmd := ContestDisputeCmd{TenantID: "bad", DisputeID: uuid.NewString(), Evidence: evidence}
		if err := uc.Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid dispute id rejected", func(t *testing.T) {
		uc := NewContestDisputeUseCase(newRepo(), fakeTx{}, newClock())
		cmd := ContestDisputeCmd{TenantID: validTenant(), DisputeID: "bad", Evidence: evidence}
		if err := uc.Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("not found", func(t *testing.T) {
		uc := NewContestDisputeUseCase(newRepo(), fakeTx{}, newClock())
		cmd := ContestDisputeCmd{TenantID: validTenant(), DisputeID: uuid.NewString(), Evidence: evidence}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrDisputeNotFound) {
			t.Fatalf("expected ErrDisputeNotFound, got %v", err)
		}
	})

	t.Run("cross-tenant denied", func(t *testing.T) {
		r := newRepo()
		d := seedOpen(r, tenantID(t), future)
		uc := NewContestDisputeUseCase(r, fakeTx{}, newClock())
		cmd := ContestDisputeCmd{TenantID: validTenant(), DisputeID: d.ID().String(), Evidence: evidence}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrDisputeNotFound) {
			t.Fatalf("expected ErrDisputeNotFound, got %v", err)
		}
	})

	t.Run("domain rule error surfaces (no evidence)", func(t *testing.T) {
		r := newRepo()
		tid := tenantID(t)
		d := seedOpen(r, tid, future)
		uc := NewContestDisputeUseCase(r, fakeTx{}, newClock())
		cmd := ContestDisputeCmd{TenantID: tid.String(), DisputeID: d.ID().String(), Evidence: nil}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrEvidenceRequired) {
			t.Fatalf("expected ErrEvidenceRequired, got %v", err)
		}
	})

	t.Run("find error propagated", func(t *testing.T) {
		r := newRepo()
		r.findErr = errBoom
		uc := NewContestDisputeUseCase(r, fakeTx{}, newClock())
		cmd := ContestDisputeCmd{TenantID: validTenant(), DisputeID: uuid.NewString(), Evidence: evidence}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("update error propagated", func(t *testing.T) {
		r := newRepo()
		tid := tenantID(t)
		d := seedOpen(r, tid, future)
		r.updateErr = errBoom
		uc := NewContestDisputeUseCase(r, fakeTx{}, newClock())
		cmd := ContestDisputeCmd{TenantID: tid.String(), DisputeID: d.ID().String(), Evidence: evidence}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

// ── AcceptDispute ─────────────────────────────────────────────────────────────

func TestAcceptDisputeUseCase_Execute(t *testing.T) {
	future := testNow.Add(24 * time.Hour)

	t.Run("accepts and publishes", func(t *testing.T) {
		r := newRepo()
		tid := tenantID(t)
		d := seedOpen(r, tid, future)
		p := &fakePublisher{}
		uc := NewAcceptDisputeUseCase(r, fakeTx{}, p, newClock())

		cmd := AcceptDisputeCmd{TenantID: tid.String(), DisputeID: d.ID().String(), Note: "acepto"}
		if err := uc.Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.updated) != 1 || r.updated[0].Status() != domain.StatusAccepted {
			t.Errorf("not accepted: %+v", r.updated)
		}
		if len(p.published) != 1 {
			t.Errorf("expected 1 event, got %d", len(p.published))
		}
	})

	t.Run("invalid tenant rejected", func(t *testing.T) {
		uc := NewAcceptDisputeUseCase(newRepo(), fakeTx{}, &fakePublisher{}, newClock())
		if err := uc.Execute(context.Background(), AcceptDisputeCmd{TenantID: "bad", DisputeID: uuid.NewString()}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid dispute id rejected", func(t *testing.T) {
		uc := NewAcceptDisputeUseCase(newRepo(), fakeTx{}, &fakePublisher{}, newClock())
		if err := uc.Execute(context.Background(), AcceptDisputeCmd{TenantID: validTenant(), DisputeID: "bad"}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("cross-tenant denied", func(t *testing.T) {
		r := newRepo()
		d := seedOpen(r, tenantID(t), future)
		uc := NewAcceptDisputeUseCase(r, fakeTx{}, &fakePublisher{}, newClock())
		cmd := AcceptDisputeCmd{TenantID: validTenant(), DisputeID: d.ID().String()}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrDisputeNotFound) {
			t.Fatalf("expected ErrDisputeNotFound, got %v", err)
		}
	})

	t.Run("domain error surfaces (already resolved)", func(t *testing.T) {
		r := newRepo()
		tid := tenantID(t)
		d := seedOpen(r, tid, future)
		_ = d.Accept("", testNow)
		d.PullEvents()
		uc := NewAcceptDisputeUseCase(r, fakeTx{}, &fakePublisher{}, newClock())
		cmd := AcceptDisputeCmd{TenantID: tid.String(), DisputeID: d.ID().String()}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("expected ErrInvalidTransition, got %v", err)
		}
	})

	t.Run("find error propagated", func(t *testing.T) {
		r := newRepo()
		r.findErr = errBoom
		uc := NewAcceptDisputeUseCase(r, fakeTx{}, &fakePublisher{}, newClock())
		cmd := AcceptDisputeCmd{TenantID: validTenant(), DisputeID: uuid.NewString()}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("update error propagated", func(t *testing.T) {
		r := newRepo()
		tid := tenantID(t)
		d := seedOpen(r, tid, future)
		r.updateErr = errBoom
		uc := NewAcceptDisputeUseCase(r, fakeTx{}, &fakePublisher{}, newClock())
		cmd := AcceptDisputeCmd{TenantID: tid.String(), DisputeID: d.ID().String()}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

// ── ResolveDispute ────────────────────────────────────────────────────────────

func TestResolveDisputeUseCase_Execute(t *testing.T) {
	future := testNow.Add(24 * time.Hour)

	// seedUnderReview crea una disputa ya contestada (under_review).
	seedUnderReview := func(r *fakeRepo, tid domain.TenantID) *domain.Dispute {
		d := seedOpen(r, tid, future)
		ev, err := domain.NewEvidence(domain.NewEvidenceID(), "receipt", "r", "d", testNow)
		if err != nil {
			t.Fatalf("evidence: %v", err)
		}
		if err := d.Contest([]domain.Evidence{ev}, "", testNow); err != nil {
			t.Fatalf("contest: %v", err)
		}
		d.PullEvents()
		return d
	}

	t.Run("resolves won and publishes", func(t *testing.T) {
		r := newRepo()
		d := seedUnderReview(r, tenantID(t))
		p := &fakePublisher{}
		uc := NewResolveDisputeUseCase(r, fakeTx{}, p, newClock())

		cmd := ResolveDisputeCmd{DisputeID: d.ID().String(), Outcome: "won", Note: "ganada"}
		if err := uc.Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.updated) != 1 || r.updated[0].Status() != domain.StatusWon {
			t.Errorf("not resolved won: %+v", r.updated)
		}
		if len(p.published) != 1 {
			t.Errorf("expected 1 event, got %d", len(p.published))
		}
	})

	t.Run("invalid dispute id rejected", func(t *testing.T) {
		uc := NewResolveDisputeUseCase(newRepo(), fakeTx{}, &fakePublisher{}, newClock())
		if err := uc.Execute(context.Background(), ResolveDisputeCmd{DisputeID: "bad", Outcome: "won"}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid outcome rejected", func(t *testing.T) {
		uc := NewResolveDisputeUseCase(newRepo(), fakeTx{}, &fakePublisher{}, newClock())
		cmd := ResolveDisputeCmd{DisputeID: uuid.NewString(), Outcome: "bogus"}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrInvalidResolutionOutcome) {
			t.Fatalf("expected ErrInvalidResolutionOutcome, got %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		uc := NewResolveDisputeUseCase(newRepo(), fakeTx{}, &fakePublisher{}, newClock())
		cmd := ResolveDisputeCmd{DisputeID: uuid.NewString(), Outcome: "won"}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrDisputeNotFound) {
			t.Fatalf("expected ErrDisputeNotFound, got %v", err)
		}
	})

	t.Run("domain error surfaces (resolve from open)", func(t *testing.T) {
		r := newRepo()
		d := seedOpen(r, tenantID(t), future) // sigue en open
		uc := NewResolveDisputeUseCase(r, fakeTx{}, &fakePublisher{}, newClock())
		cmd := ResolveDisputeCmd{DisputeID: d.ID().String(), Outcome: "won"}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("expected ErrInvalidTransition, got %v", err)
		}
	})

	t.Run("update error propagated", func(t *testing.T) {
		r := newRepo()
		d := seedUnderReview(r, tenantID(t))
		r.updateErr = errBoom
		uc := NewResolveDisputeUseCase(r, fakeTx{}, &fakePublisher{}, newClock())
		cmd := ResolveDisputeCmd{DisputeID: d.ID().String(), Outcome: "lost"}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

// ── GetDispute / ListDisputes ─────────────────────────────────────────────────

func TestGetDisputeUseCase_Execute(t *testing.T) {
	future := testNow.Add(24 * time.Hour)

	t.Run("returns view for owned dispute", func(t *testing.T) {
		r := newRepo()
		tid := tenantID(t)
		d := seedOpen(r, tid, future)
		uc := NewGetDisputeUseCase(r, newClock())
		v, err := uc.Execute(context.Background(), GetDisputeQuery{TenantID: tid.String(), DisputeID: d.ID().String()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v.ID != d.ID().String() || v.Status != "open" {
			t.Errorf("unexpected view: %+v", v)
		}
	})

	t.Run("invalid tenant rejected", func(t *testing.T) {
		uc := NewGetDisputeUseCase(newRepo(), newClock())
		if _, err := uc.Execute(context.Background(), GetDisputeQuery{TenantID: "bad", DisputeID: uuid.NewString()}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid dispute id rejected", func(t *testing.T) {
		uc := NewGetDisputeUseCase(newRepo(), newClock())
		if _, err := uc.Execute(context.Background(), GetDisputeQuery{TenantID: validTenant(), DisputeID: "bad"}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("not found", func(t *testing.T) {
		uc := NewGetDisputeUseCase(newRepo(), newClock())
		_, err := uc.Execute(context.Background(), GetDisputeQuery{TenantID: validTenant(), DisputeID: uuid.NewString()})
		if !errors.Is(err, domain.ErrDisputeNotFound) {
			t.Fatalf("expected ErrDisputeNotFound, got %v", err)
		}
	})

	t.Run("cross-tenant denied", func(t *testing.T) {
		r := newRepo()
		d := seedOpen(r, tenantID(t), future)
		uc := NewGetDisputeUseCase(r, newClock())
		_, err := uc.Execute(context.Background(), GetDisputeQuery{TenantID: validTenant(), DisputeID: d.ID().String()})
		if !errors.Is(err, domain.ErrDisputeNotFound) {
			t.Fatalf("expected ErrDisputeNotFound, got %v", err)
		}
	})
}

func TestListDisputesUseCase_Execute(t *testing.T) {
	future := testNow.Add(24 * time.Hour)

	t.Run("maps disputes to views", func(t *testing.T) {
		r := newRepo()
		tid := tenantID(t)
		r.listed = []*domain.Dispute{seedOpen(r, tid, future)}
		uc := NewListDisputesUseCase(r, newClock())
		views, err := uc.Execute(context.Background(), ListDisputesQuery{TenantID: tid.String(), StatusFilter: "open", Limit: 10})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(views) != 1 {
			t.Fatalf("expected 1 view, got %d", len(views))
		}
		if r.gotStatusFilter != "open" || r.gotLimit != 10 {
			t.Errorf("filter/limit not forwarded: %q / %d", r.gotStatusFilter, r.gotLimit)
		}
	})

	t.Run("default limit when non-positive", func(t *testing.T) {
		r := newRepo()
		tid := tenantID(t)
		uc := NewListDisputesUseCase(r, newClock())
		if _, err := uc.Execute(context.Background(), ListDisputesQuery{TenantID: tid.String(), Limit: 0}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.gotLimit != 50 {
			t.Errorf("limit = %d, want 50", r.gotLimit)
		}
	})

	t.Run("invalid tenant rejected", func(t *testing.T) {
		uc := NewListDisputesUseCase(newRepo(), newClock())
		if _, err := uc.Execute(context.Background(), ListDisputesQuery{TenantID: "bad"}); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("repo error propagated", func(t *testing.T) {
		r := newRepo()
		r.listErr = errBoom
		uc := NewListDisputesUseCase(r, newClock())
		if _, err := uc.Execute(context.Background(), ListDisputesQuery{TenantID: validTenant()}); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}

func TestToView(t *testing.T) {
	openedAt := testNow
	responded := testNow.Add(time.Hour)
	resolved := testNow.Add(2 * time.Hour)
	deadline := testNow.Add(24 * time.Hour)
	tid := domain.TenantID("tenant-1")
	ev := []domain.Evidence{domain.ReconstituteEvidence(domain.NewEvidenceID(), "receipt", "ref", "d", responded)}

	t.Run("open dispute overdue flag and nil timestamps", func(t *testing.T) {
		d := domain.ReconstituteDispute(domain.NewDisputeID(), tid, "pay-1", "psp", 5000, "ARS",
			domain.ReasonGeneral, domain.StatusOpen, nil, "", "",
			testNow.Add(-time.Hour), openedAt, nil, nil, 1) // deadline en el pasado
		v := toView(d, testNow)
		if !v.IsOverdue {
			t.Error("expected overdue")
		}
		if v.RespondedAt != nil || v.ResolvedAt != nil {
			t.Error("expected nil responded/resolved")
		}
		if len(v.Evidence) != 0 {
			t.Errorf("expected no evidence, got %d", len(v.Evidence))
		}
	})

	t.Run("resolved dispute formats timestamps and evidence", func(t *testing.T) {
		d := domain.ReconstituteDispute(domain.NewDisputeID(), tid, "pay-1", "psp", 5000, "ARS",
			domain.ReasonGeneral, domain.StatusWon, ev, "resp", "res",
			deadline, openedAt, &responded, &resolved, 3)
		v := toView(d, testNow)
		if v.RespondedAt == nil || *v.RespondedAt != responded.Format(time.RFC3339) {
			t.Errorf("respondedAt mismatch: %v", v.RespondedAt)
		}
		if v.ResolvedAt == nil || *v.ResolvedAt != resolved.Format(time.RFC3339) {
			t.Errorf("resolvedAt mismatch: %v", v.ResolvedAt)
		}
		if len(v.Evidence) != 1 || v.Evidence[0].EvidenceType != "receipt" {
			t.Errorf("evidence mapping mismatch: %+v", v.Evidence)
		}
		if v.IsOverdue {
			t.Error("resolved dispute should not be overdue")
		}
	})
}

// tenantTx registra el tenant del contexto con el que se abrió la tx (el que
// TxManager fija en app.current_tenant).
type tenantTx struct{ tenant string }

func (t *tenantTx) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	t.tenant, _ = pkgpostgres.TenantIDFromContext(ctx)
	return fn(ctx)
}
