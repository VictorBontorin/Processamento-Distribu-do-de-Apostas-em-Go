// Package auth valida tokens de acesso emitidos por um IdP externo
// (OAuth 2.0 / OIDC) e aplica a autorização por papel.
//
// Modelo de permissões:
//   - papel "provider": envia operações de aposta e consulta somente as
//     próprias transações; o providerId vem do claim "providerId" do token,
//     nunca do corpo da requisição;
//   - papel "internal": operações de carteira (abertura, consulta, ledger,
//     reconciliação), restritas ao serviço interno.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

const (
	RoleProvider = "provider"
	RoleInternal = "internal"
)

var ErrInvalidToken = errors.New("invalid token")

// Principal é a identidade autenticada extraída do token.
type Principal struct {
	Subject    string
	ClientID   string
	ProviderID string
	Roles      []string
}

func (p Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}

	return false
}

// TokenVerifier valida um token bruto (assinatura, emissor, audiência e
// expiração) e devolve a identidade.
type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (Principal, error)
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// Require autentica a requisição e exige o papel informado.
func Require(
	verifier TokenVerifier,
	role string,
	next http.Handler,
) http.Handler {
	return RequireAny(verifier, []string{role}, next)
}

// RequireAny autentica a requisição e exige ao menos um dos papéis.
// Sem credenciais ou com token inválido/expirado: 401.
// Autenticado sem nenhum dos papéis: 403. Em ambos os casos o handler não
// executa, portanto não há efeito financeiro nem exposição de dados.
func RequireAny(
	verifier TokenVerifier,
	roles []string,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")

		scheme, token, found := strings.Cut(header, " ")
		if !found || !strings.EqualFold(scheme, "Bearer") ||
			strings.TrimSpace(token) == "" {
			unauthorized(w, "missing bearer token")
			return
		}

		principal, err := verifier.Verify(r.Context(), strings.TrimSpace(token))
		if err != nil {
			unauthorized(w, "invalid or expired token")
			return
		}

		allowed := false

		for _, role := range roles {
			if principal.HasRole(role) {
				allowed = true
				break
			}
		}

		if !allowed {
			writeError(
				w,
				http.StatusForbidden,
				"FORBIDDEN",
				"insufficient permissions",
			)
			return
		}

		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
	})
}

func unauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="wager"`)
	writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", message)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": message,
		"code":  code,
	})
}
