//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"wager/internal/app"
)

// A composição Fx sobe com infraestrutura real, atende requisições e, no
// encerramento, para servidor e workers e fecha o pool de conexões.
func TestFxCompositionStartAndStop(t *testing.T) {
	db, _ := newDatabase(t)
	queues := newQueues(env.prefix + "-fx-" + randHex(2))

	t.Cleanup(func() { deleteQueues(queues) })

	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)

	for k, v := range map[string]string{
		"HTTP_ADDR":            addr,
		"DB_HOST":              env.dbHost,
		"DB_PORT":              env.dbPort,
		"DB_USER":              env.dbUser,
		"DB_PASSWORD":          env.dbPassword,
		"DB_NAME":              db,
		"SQS_ENDPOINT":         env.sqsEndpoint,
		"SQS_QUEUE_NAME":       queues.Tx,
		"SQS_DLQ_NAME":         queues.DLQ,
		"SQS_EVENT_QUEUE_NAME": queues.Events,
		"AUTH_ISSUER_URL":      env.issuer,
	} {
		t.Setenv(k, v)
	}

	var pool *pgxpool.Pool

	application := fx.New(app.Options(), fx.Populate(&pool), fx.NopLogger)

	if err := application.Err(); err != nil {
		t.Fatalf("fx graph: %v", err)
	}

	startCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := application.Start(startCtx); err != nil {
		t.Fatalf("start: %v", err)
	}

	ready := func() int {
		res, err := http.Get("http://" + addr + "/health/ready")
		if err != nil {
			return 0
		}
		defer res.Body.Close()

		return res.StatusCode
	}

	eventually(t, 30*time.Second, "readiness after start", func() bool { return ready() == http.StatusOK })

	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("pool should work while running: %v", err)
	}

	stopCtx, cancelStop := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStop()

	if err := application.Stop(stopCtx); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Recursos liberados: o servidor parou de aceitar entradas e o pool
	// foi fechado depois dos componentes que o usam.
	if ready() != 0 {
		t.Fatal("server still accepting requests after stop")
	}

	if err := pool.Ping(context.Background()); err == nil {
		t.Fatal("pool still open after stop")
	}
}
