// Package oidcverifier implementa auth.TokenVerifier com um IdP OIDC
// (Keycloak). Assinatura, emissor, audiência e expiração são validados
// com as chaves publicadas no JWKS do IdP.
package oidcverifier

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"wager/internal/auth"
)

type Config struct {
	// IssuerURL é o valor esperado no claim "iss" dos tokens.
	IssuerURL string
	// DiscoveryURL é a URL usada para buscar a configuração OIDC/JWKS.
	// Vazio = igual ao IssuerURL. Útil no Docker, onde o serviço acessa
	// o IdP por um host diferente do que aparece no token.
	DiscoveryURL string
	// Audience é o valor exigido no claim "aud".
	Audience string
}

func LoadConfig() Config {
	return Config{
		IssuerURL:    getenv("AUTH_ISSUER_URL", "http://localhost:8081/realms/wager"),
		DiscoveryURL: os.Getenv("AUTH_DISCOVERY_URL"),
		Audience:     getenv("AUTH_AUDIENCE", "wager-api"),
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}

type verifier struct {
	v *oidc.IDTokenVerifier
}

type claims struct {
	Azp         string `json:"azp"`
	ProviderID  string `json:"providerId"`
	RealmAccess struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

// New valida a configuração e descobre o IdP (com tentativas, pois o
// Keycloak pode demorar a subir). Falha na inicialização se o IdP
// não responder.
func New(cfg Config) (auth.TokenVerifier, error) {
	if cfg.IssuerURL == "" || cfg.Audience == "" {
		return nil, fmt.Errorf("auth: issuer and audience are required")
	}

	// O contexto do provider é retido para renovar o JWKS; por isso não
	// pode ter prazo nem ser cancelado. O timeout fica no http.Client.
	ctx := oidc.ClientContext(
		context.Background(),
		&http.Client{Timeout: 10 * time.Second},
	)

	discovery := cfg.DiscoveryURL
	if discovery == "" {
		discovery = cfg.IssuerURL
	}

	if discovery != cfg.IssuerURL {
		ctx = oidc.InsecureIssuerURLContext(ctx, cfg.IssuerURL)
	}

	var (
		provider *oidc.Provider
		err      error
	)

	for attempt := 1; attempt <= 30; attempt++ {
		provider, err = oidc.NewProvider(ctx, discovery)
		if err == nil {
			break
		}

		time.Sleep(2 * time.Second)
	}

	if err != nil {
		return nil, fmt.Errorf("auth: discover identity provider: %w", err)
	}

	return &verifier{
		v: provider.Verifier(&oidc.Config{ClientID: cfg.Audience}),
	}, nil
}

func (v *verifier) Verify(
	ctx context.Context,
	rawToken string,
) (auth.Principal, error) {
	token, err := v.v.Verify(ctx, rawToken)
	if err != nil {
		return auth.Principal{}, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
	}

	var c claims
	if err := token.Claims(&c); err != nil {
		return auth.Principal{}, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
	}

	return auth.Principal{
		Subject:    token.Subject,
		ClientID:   c.Azp,
		ProviderID: c.ProviderID,
		Roles:      c.RealmAccess.Roles,
	}, nil
}
