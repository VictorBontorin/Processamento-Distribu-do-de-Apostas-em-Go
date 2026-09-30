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
			return &fxevent.SlogLogger{Logger: slog.Default()}
		}),
	).Run()
}
