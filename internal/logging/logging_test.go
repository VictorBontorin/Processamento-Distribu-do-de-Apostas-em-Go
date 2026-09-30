package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"wager/internal/correlation"
	"wager/internal/domain"
)

func capture(t *testing.T, fn func(ctx context.Context, logger *slog.Logger)) map[string]any {
	t.Helper()

	var buf bytes.Buffer

	logger := slog.New(contextHandler{
		inner: slog.NewJSONHandler(&buf, &slog.HandlerOptions{ReplaceAttr: redact}),
	})

	fn(context.Background(), logger)

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("log is not JSON: %v (%q)", err, buf.String())
	}

	return out
}

func TestLogsCarryCorrelationAndContextAttributes(t *testing.T) {
	id := domain.NewID()

	out := capture(t, func(ctx context.Context, logger *slog.Logger) {
		ctx = correlation.WithID(ctx, id)
		ctx = WithAttrs(ctx, "walletId", "w-1", "providerId", "provider-a")
		ctx = WithAttrs(ctx, "messageId", "msg-1")

		logger.InfoContext(ctx, "hello")
	})

	want := map[string]string{
		"correlationId": id.String(),
		"walletId":      "w-1",
		"providerId":    "provider-a",
		"messageId":     "msg-1",
		"msg":           "hello",
	}

	for key, value := range want {
		if out[key] != value {
			t.Fatalf("%s = %v, want %s (log: %v)", key, out[key], value, out)
		}
	}
}

func TestSensitiveKeysAreRedacted(t *testing.T) {
	out := capture(t, func(ctx context.Context, logger *slog.Logger) {
		logger.InfoContext(ctx, "x", "authorization", "Bearer abc", "clientSecret", "s3cr3t", "transactionId", "t-1")
	})

	if out["authorization"] != "[REDACTED]" || out["clientSecret"] != "[REDACTED]" {
		t.Fatalf("sensitive values leaked: %v", out)
	}

	if out["transactionId"] != "t-1" {
		t.Fatalf("regular attribute changed: %v", out)
	}
}
