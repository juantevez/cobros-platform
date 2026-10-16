package risk

import (
	"context"
	"testing"

	"github.com/juantevez/cobros-platform/context/payment/application"
)

func TestPermissiveEvaluator_Evaluate(t *testing.T) {
	got, err := NewPermissiveEvaluator().Evaluate(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := (application.RiskDecision{Approved: true}); got != want {
		t.Errorf("decision = %+v, want %+v", got, want)
	}
}
