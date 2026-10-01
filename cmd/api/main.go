package main

import (
	"log/slog"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"wager/internal/app"
	"wager/internal/logging"
)

func main() {
	logging.Setup()

	fx.New(
		app.Options(),
		fx.WithLogger(func() fxevent.Logger {
			// Eventos internos do Fx em debug (erros continuam em error).
			logger := &fxevent.SlogLogger{Logger: slog.Default()}
			logger.UseLogLevel(slog.LevelDebug)

			return logger
		}),
	).Run()
}
