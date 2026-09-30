package app

import (
	"testing"

	"go.uber.org/fx"
)

// Valida o grafo de dependências do Fx sem executar construtores
// (não exige PostgreSQL, SQS nem Keycloak).
func TestFxGraphIsValid(t *testing.T) {
	if err := fx.ValidateApp(Options()); err != nil {
		t.Fatalf("invalid Fx graph: %v", err)
	}
}
