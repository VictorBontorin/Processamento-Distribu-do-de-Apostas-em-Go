// Package correlation propaga o correlationId da requisição (HTTP ou SQS)
// até os registros da outbox, sem acoplar o domínio.
package correlation

import (
	"context"
	stdhttp "net/http"

	"wager/internal/domain"
)

const Header = "X-Correlation-Id"

type ctxKey struct{}

// WithID devolve um contexto carregando o correlationId.
func WithID(ctx context.Context, id domain.ID) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext devolve o correlationId do contexto, ou zero se ausente.
func FromContext(ctx context.Context) domain.ID {
	id, _ := ctx.Value(ctxKey{}).(domain.ID)
	return id
}

// Middleware aceita um X-Correlation-Id UUID válido ou gera um novo, expõe
// o valor na resposta e o coloca no contexto da requisição.
func Middleware(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		id, err := domain.ParseID(r.Header.Get(Header))
		if err != nil || id.IsZero() {
			id = domain.NewID()
		}

		w.Header().Set(Header, id.String())

		next.ServeHTTP(w, r.WithContext(WithID(r.Context(), id)))
	})
}
