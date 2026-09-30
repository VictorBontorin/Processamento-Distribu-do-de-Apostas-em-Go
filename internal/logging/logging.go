// Package logging configura logs JSON estruturados (log/slog). Todo log
// emitido com um contexto carrega automaticamente o correlationId e os
// atributos adicionados com WithAttrs (messageId, walletId, providerId...).
//
// Nunca registre credenciais, tokens nem payloads financeiros completos:
// use identificadores e estados. Chaves sensíveis são mascaradas por
// segurança adicional.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"wager/internal/correlation"
)

type attrsKey struct{}

// WithAttrs devolve um contexto cujos logs incluem os atributos dados.
func WithAttrs(ctx context.Context, args ...any) context.Context {
	existing, _ := ctx.Value(attrsKey{}).([]slog.Attr)

	merged := make([]slog.Attr, 0, len(existing)+len(args)/2)
	merged = append(merged, existing...)
	merged = append(merged, toAttrs(args)...)

	return context.WithValue(ctx, attrsKey{}, merged)
}

func toAttrs(args []any) []slog.Attr {
	record := slog.NewRecord(time.Time{}, slog.LevelInfo, "", 0)
	record.Add(args...)

	var out []slog.Attr

	record.Attrs(func(a slog.Attr) bool {
		out = append(out, a)
		return true
	})

	return out
}

type contextHandler struct {
	inner slog.Handler
}

func (h contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h contextHandler) Handle(ctx context.Context, record slog.Record) error {
	if id := correlation.FromContext(ctx); !id.IsZero() {
		record.AddAttrs(slog.String("correlationId", id.String()))
	}

	if attrs, ok := ctx.Value(attrsKey{}).([]slog.Attr); ok {
		record.AddAttrs(attrs...)
	}

	return h.inner.Handle(ctx, record)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{inner: h.inner.WithGroup(name)}
}

var sensitiveKeys = []string{"authorization", "password", "secret", "token", "credential"}

func redact(_ []string, a slog.Attr) slog.Attr {
	key := strings.ToLower(a.Key)

	for _, sensitive := range sensitiveKeys {
		if strings.Contains(key, sensitive) {
			return slog.String(a.Key, "[REDACTED]")
		}
	}

	return a
}

// New cria o logger JSON escrevendo em w-equivalente (stdout), no nível dado.
func New(level slog.Level) *slog.Logger {
	return slog.New(contextHandler{
		inner: slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level:       level,
			ReplaceAttr: redact,
		}),
	})
}

// Setup instala o logger JSON como padrão. Nível via LOG_LEVEL
// (debug, info, warn, error; padrão info).
func Setup() {
	level := slog.LevelInfo

	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	slog.SetDefault(New(level))
}
