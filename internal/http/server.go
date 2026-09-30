package http

import (
	"context"
	"net/http"
	"os"

	"go.uber.org/fx"

	"wager/internal/auth"
	"wager/internal/correlation"
	"wager/internal/metrics"
)

type Server struct {
	server  *http.Server
	wallets *WalletHandler
	wager   *WagerHandler
}

func NewServer(
	lc fx.Lifecycle,
	wallets *WalletHandler,
	wager *WagerHandler,
	read *ReadHandler,
	health *HealthHandler,
	verifier auth.TokenVerifier,
) *Server {
	mux := http.NewServeMux()

	provider := func(h http.HandlerFunc) http.Handler {
		return auth.Require(verifier, auth.RoleProvider, h)
	}

	internal := func(h http.HandlerFunc) http.Handler {
		return auth.Require(verifier, auth.RoleInternal, h)
	}

	providerOrInternal := func(h http.HandlerFunc) http.Handler {
		return auth.RequireAny(
			verifier,
			[]string{auth.RoleProvider, auth.RoleInternal},
			h,
		)
	}

	// Endpoints de provedores de jogos.
	mux.Handle("POST /wagering/transactions", provider(wager.Create))
	mux.Handle(
		"GET /wagering/transactions/{transactionId}",
		providerOrInternal(read.GetTransaction),
	)
	mux.Handle(
		"GET /providers/{providerId}/wagering/transactions/{externalTransactionId}",
		providerOrInternal(read.GetProviderTransaction),
	)

	// Endpoints de carteira: somente serviço interno.
	mux.Handle("POST /wallets", internal(wallets.Create))
	mux.Handle("GET /wallets/{walletId}", internal(wallets.Get))
	mux.Handle("GET /wallets/{walletId}/ledger", internal(read.GetLedger))
	mux.Handle("POST /wallets/{walletId}/reconciliation", internal(read.Reconcile))

	// Público.
	mux.Handle("GET /metrics", metrics.Handler())
	mux.HandleFunc("GET /health/live", health.Live)
	mux.HandleFunc("GET /health/ready", health.Ready)

	server := &Server{
		wallets: wallets,
		wager:   wager,
		server: &http.Server{
			Addr:    httpAddr(),
			Handler: correlation.Middleware(observe(mux)),
		},
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go func() {
				if err := server.server.ListenAndServe(); err != nil &&
					err != http.ErrServerClosed {
					panic(err)
				}
			}()

			return nil
		},
		OnStop: func(ctx context.Context) error {
			return server.server.Shutdown(ctx)
		},
	})

	return server
}

// httpAddr lê o endereço de escuta de HTTP_ADDR (padrão :8080).
func httpAddr() string {
	if addr := os.Getenv("HTTP_ADDR"); addr != "" {
		return addr
	}

	return ":8080"
}
