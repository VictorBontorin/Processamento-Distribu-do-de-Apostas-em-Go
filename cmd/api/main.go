package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"wager/internal/application"
	"wager/internal/domain"
	"wager/internal/auth/oidcverifier"
	wagerServer "wager/internal/http"
	"wager/internal/infrastructure/outbox"
	"wager/internal/infrastructure/pendingreference"
	"wager/internal/infrastructure/postgres"
	"wager/internal/infrastructure/sqs"
)

func main() {
	fx.New(
		fx.Provide(
			// Autenticação (IdP OIDC)
			oidcverifier.LoadConfig,
			oidcverifier.New,

			// HTTP
			wagerServer.NewWagerHandler,
			wagerServer.NewServer,
			wagerServer.NewWalletHandler,
			wagerServer.NewReadHandler,
			wagerServer.NewHealthHandler,

			// PostgreSQL
			providePostgresConfig,
			providePostgresPool,
			postgres.NewUnitOfWork,
			postgres.NewWagerUnitOfWork,
			postgres.NewWalletRepository,
			postgres.NewLedgerRepository,
			postgres.NewWagerTransactionRepository,
			postgres.NewWagerStore,
			postgres.NewReadStore,
			postgres.NewOutboxRepository,

			// Application
			application.NewWagerService,

			// SQS
			provideSQSClient,
			sqs.NewConsumer,

			// Outbox
			outbox.NewPublisher,

			// Referências pendentes
			pendingreference.NewWorker,

			// Interfaces
			provideWalletRepository,
			provideWalletOpener,
			provideHTTPWagerProcessor,
			provideSQSProcessor,
		),

		fx.Invoke(
			registerLifecycle,
			registerOutboxLifecycle,
			registerPendingReferenceLifecycle,
			func(*wagerServer.Server) {},
			func(*sqs.Consumer) {},
		),
	).Run()
}

func provideWalletRepository(
	repo *postgres.WalletRepository,
) interface {
	GetByID(context.Context, domain.ID) (*domain.Wallet, error)
	Save(context.Context, *domain.Wallet) error
} {
	return repo
}

func provideHTTPWagerProcessor(
	store *postgres.WagerStore,
) interface {
	ProcessExternal(
		context.Context,
		*domain.WagerTransaction,
		time.Time,
	) (postgres.ProcessResult, error)
} {
	return store
}

func provideSQSProcessor(
	store *postgres.WagerStore,
) sqs.WagerProcessor {
	return store
}

func providePostgresConfig() postgres.Config {
	return postgres.Config{
		Host:     "localhost",
		Port:     "5432",
		User:     "wager",
		Password: "wager",
		Database: "wager",
	}
}

func providePostgresPool(
	lc fx.Lifecycle,
	cfg postgres.Config,
) (*pgxpool.Pool, error) {
	ctx := context.Background()

	pool, err := postgres.NewPool(ctx, cfg)
	if err != nil {
		return nil, err
	}

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			pool.Close()
			return nil
		},
	})

	return pool, nil
}

func provideSQSClient(
	lc fx.Lifecycle,
) (*sqs.Client, error) {
	ctx := context.Background()

	client, err := sqs.NewClient(ctx, sqs.LoadConfig())
	if err != nil {
		return nil, err
	}

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return client.Close(ctx)
		},
	})

	return client, nil
}

func registerLifecycle(lc fx.Lifecycle) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return nil
		},
	})
}

func registerOutboxLifecycle(
	lc fx.Lifecycle,
	publisher *outbox.Publisher,
) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			publisher.Start(ctx)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return publisher.Stop(ctx)
		},
	})
}

func registerPendingReferenceLifecycle(
	lc fx.Lifecycle,
	worker *pendingreference.Worker,
) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			worker.Start(ctx)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return worker.Stop(ctx)
		},
	})
}

func provideWalletOpener(
	store *postgres.WagerStore,
) wagerServer.WalletOpener {
	return store
}
