//go:build integration

// Testes de integração: exigem PostgreSQL, LocalStack (SQS) e Keycloak
// em execução (docker compose up -d postgres localstack keycloak).
// Executar com: go test -tags=integration -race -count=1 ./test/integration/...
package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	providerA = "provider-a"
	providerB = "provider-b"
)

type queueNames struct {
	Tx, DLQ, Events string
}

var env struct {
	repoRoot    string
	binary      string
	prefix      string
	dbHost      string
	dbPort      string
	dbUser      string
	dbPassword  string
	adminDB     string
	dbName      string // banco compartilhado pelas instâncias do pacote
	sqsEndpoint string
	issuer      string
	keycloak    string
	queues      queueNames // filas compartilhadas
	admin       *pgxpool.Pool
	pool        *pgxpool.Pool // pool do banco compartilhado
}

// shared são as instâncias independentes (processos distintos, conexões e
// memória próprias) usadas pela maioria dos testes.
var shared []*instance

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	env.repoRoot = root
	env.prefix = "it" + randHex(4)
	env.dbHost = getenv("TEST_DB_HOST", "localhost")
	env.dbPort = getenv("TEST_DB_PORT", "5432")
	env.dbUser = getenv("TEST_DB_USER", "wager")
	env.dbPassword = getenv("TEST_DB_PASSWORD", "wager")
	env.adminDB = getenv("TEST_DB_ADMIN_DB", "wager")
	env.sqsEndpoint = getenv("SQS_ENDPOINT", "http://localhost:4566")
	env.keycloak = getenv("KEYCLOAK_URL", "http://localhost:8081")
	env.issuer = getenv("AUTH_ISSUER_URL", env.keycloak+"/realms/wager")
	env.queues = newQueues(env.prefix + "-shared")

	env.admin, err = pgxpool.New(ctx, dsn(env.adminDB))
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect admin postgres:", err)
		return 1
	}
	defer env.admin.Close()

	if err := env.admin.Ping(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "ping admin postgres (docker compose up -d postgres?):", err)
		return 1
	}

	env.dbName, err = createDatabase(ctx, env.prefix+"_shared")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create database:", err)
		return 1
	}
	defer dropDatabase(env.dbName)

	env.pool, err = pgxpool.New(ctx, dsn(env.dbName))
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect test database:", err)
		return 1
	}
	defer env.pool.Close()

	tmp, err := os.MkdirTemp("", "wager-it")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(tmp)

	env.binary = filepath.Join(tmp, "api")
	if runtime.GOOS == "windows" {
		env.binary += ".exe"
	}

	build := exec.Command("go", "build", "-o", env.binary, "./cmd/api")
	build.Dir = root
	build.Stdout, build.Stderr = os.Stdout, os.Stderr

	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build api:", err)
		return 1
	}

	for i := 0; i < 3; i++ {
		inst, err := startInstance(instanceOpts{
			name:   fmt.Sprintf("shared-%d", i),
			db:     env.dbName,
			queues: env.queues,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "start shared instance:", err)
			stopAll()

			return 1
		}

		shared = append(shared, inst)
	}

	code := m.Run()

	stopAll()
	deleteQueues(env.queues)

	return code
}

func stopAll() {
	for _, inst := range shared {
		inst.stop()
	}
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}

		dir = parent
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	h := hex.EncodeToString(b)

	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// ---------- banco ----------

func dsn(db string) string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s",
		env.dbUser, env.dbPassword, env.dbHost, env.dbPort, db,
	)
}

func createDatabase(ctx context.Context, name string) (string, error) {
	if _, err := env.admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return "", err
	}

	pool, err := pgxpool.New(ctx, dsn(name))
	if err != nil {
		return "", err
	}
	defer pool.Close()

	files, err := filepath.Glob(filepath.Join(env.repoRoot, "migrations", "*.sql"))
	if err != nil {
		return "", err
	}

	sort.Strings(files)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Release()

	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}

		// Protocolo simples: permite várias instruções por arquivo.
		if _, err := conn.Conn().PgConn().Exec(ctx, string(content)).ReadAll(); err != nil {
			return "", fmt.Errorf("apply %s: %w", filepath.Base(file), err)
		}
	}

	return name, nil
}

func dropDatabase(name string) {
	_, _ = env.admin.Exec(
		context.Background(),
		"DROP DATABASE IF EXISTS "+name+" WITH (FORCE)",
	)
}

// newDatabase cria um banco isolado (com migrations) para um teste.
func newDatabase(t *testing.T) (string, *pgxpool.Pool) {
	t.Helper()

	name, err := createDatabase(context.Background(), env.prefix+"_"+randHex(3))
	if err != nil {
		t.Fatalf("create database: %v", err)
	}

	pool, err := pgxpool.New(context.Background(), dsn(name))
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		dropDatabase(name)
	})

	return name, pool
}

func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()

	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count query: %v", err)
	}

	return n
}

// ---------- instâncias ----------

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

type instanceOpts struct {
	name   string
	db     string
	queues queueNames
	env    map[string]string
}

type instance struct {
	name   string
	url    string
	cmd    *exec.Cmd
	done   chan struct{}
	exit   error
	output *syncBuffer
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port, nil
}

func startInstance(o instanceOpts) (*instance, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)

	cmd := exec.Command(env.binary)
	cmd.Env = append(os.Environ(),
		"HTTP_ADDR="+addr,
		"DB_HOST="+env.dbHost,
		"DB_PORT="+env.dbPort,
		"DB_USER="+env.dbUser,
		"DB_PASSWORD="+env.dbPassword,
		"DB_NAME="+o.db,
		"SQS_ENDPOINT="+env.sqsEndpoint,
		"SQS_QUEUE_NAME="+o.queues.Tx,
		"SQS_DLQ_NAME="+o.queues.DLQ,
		"SQS_EVENT_QUEUE_NAME="+o.queues.Events,
		"AUTH_ISSUER_URL="+env.issuer,
	)

	for k, v := range o.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	out := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = out, out

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	inst := &instance{
		name:   o.name,
		url:    "http://" + addr,
		cmd:    cmd,
		done:   make(chan struct{}),
		output: out,
	}

	go func() {
		inst.exit = cmd.Wait()
		close(inst.done)
	}()

	deadline := time.Now().Add(120 * time.Second)

	for time.Now().Before(deadline) {
		select {
		case <-inst.done:
			return nil, fmt.Errorf("instance %s exited during startup: %v\n%s", o.name, inst.exit, out.String())
		default:
		}

		resp, err := http.Get(inst.url + "/health/ready")
		if err == nil {
			_ = resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				return inst, nil
			}
		}

		time.Sleep(500 * time.Millisecond)
	}

	inst.kill()

	return nil, fmt.Errorf("instance %s not ready in time\n%s", o.name, out.String())
}

// stop envia SIGTERM (shutdown gracioso) e aguarda o término.
func (i *instance) stop() {
	select {
	case <-i.done:
		return
	default:
	}

	// Windows não suporta SIGTERM: nesse caso encerra à força.
	if err := i.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		i.kill()
		return
	}

	select {
	case <-i.done:
	case <-time.After(30 * time.Second):
		i.kill()
	}
}

// kill encerra o processo sem shutdown (kill -9).
func (i *instance) kill() {
	_ = i.cmd.Process.Kill()

	select {
	case <-i.done:
	case <-time.After(10 * time.Second):
	}
}

func (i *instance) waitExit(d time.Duration) bool {
	select {
	case <-i.done:
		return true
	case <-time.After(d):
		return false
	}
}

func startTest(t *testing.T, o instanceOpts) *instance {
	t.Helper()

	inst, err := startInstance(o)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		inst.stop()

		if t.Failed() {
			t.Logf("logs of %s:\n%s", inst.name, inst.output.String())
		}
	})

	return inst
}

// ---------- HTTP ----------

type resp struct {
	status int
	body   map[string]any
	raw    string
	header http.Header
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

// do é seguro para uso em goroutines (não chama FailNow).
func do(t *testing.T, method, url, tok string, headers map[string]string, body any) resp {
	t.Helper()

	var reader io.Reader

	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Errorf("marshal body: %v", err)
			return resp{}
		}

		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Errorf("new request: %v", err)
		return resp{}
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	res, err := httpClient.Do(req)
	if err != nil {
		t.Errorf("%s %s: %v", method, url, err)
		return resp{}
	}
	defer res.Body.Close()

	raw, _ := io.ReadAll(res.Body)

	out := resp{status: res.StatusCode, raw: string(raw), header: res.Header}
	_ = json.Unmarshal(raw, &out.body)

	return out
}

func (r resp) str(keys ...string) string {
	var cur any = r.body

	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}

		cur = m[k]
	}

	s, _ := cur.(string)

	return s
}

func (r resp) boolean(key string) bool {
	b, _ := r.body[key].(bool)
	return b
}

// ---------- tokens ----------

var (
	tokenMu    sync.Mutex
	tokenCache = map[string]tokenEntry{}
)

type tokenEntry struct {
	value   string
	expires time.Time
}

func fetchToken(t *testing.T, clientID, secret string) string {
	t.Helper()

	tokenMu.Lock()
	defer tokenMu.Unlock()

	if e, ok := tokenCache[clientID]; ok && time.Now().Before(e.expires) {
		return e.value
	}

	res, err := http.PostForm(
		env.keycloak+"/realms/wager/protocol/openid-connect/token",
		url.Values{
			"grant_type":    {"client_credentials"},
			"client_id":     {clientID},
			"client_secret": {secret},
		},
	)
	if err != nil {
		t.Fatalf("token request: %v", err)
	}
	defer res.Body.Close()

	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}

	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil || payload.AccessToken == "" {
		t.Fatalf("token response status=%d err=%v", res.StatusCode, err)
	}

	tokenCache[clientID] = tokenEntry{
		value:   payload.AccessToken,
		expires: time.Now().Add(time.Duration(payload.ExpiresIn/2) * time.Second),
	}

	return payload.AccessToken
}

func tokProviderA(t *testing.T) string {
	return fetchToken(t, "provider-a", "provider-a-secret")
}

func tokProviderB(t *testing.T) string {
	return fetchToken(t, "provider-b", "provider-b-secret")
}

func tokInternal(t *testing.T) string {
	return fetchToken(t, "wallet-service", "wallet-service-secret")
}

// ---------- fluxos de negócio ----------

func money(amount string) map[string]string {
	return map[string]string{"amount": amount, "currency": "BRL"}
}

func createWalletOn(t *testing.T, inst *instance, amount string) (walletID, playerID string) {
	t.Helper()

	playerID = newUUID()

	r := do(t, "POST", inst.url+"/wallets", tokInternal(t), nil, map[string]any{
		"playerId":       playerID,
		"initialBalance": money(amount),
	})
	if r.status != http.StatusCreated {
		t.Fatalf("create wallet: status=%d body=%s", r.status, r.raw)
	}

	return r.str("id"), playerID
}

func createWallet(t *testing.T, amount string) (walletID, playerID string) {
	return createWalletOn(t, shared[0], amount)
}

type txReq struct {
	provider string
	ext      string
	key      string
	walletID string
	playerID string
	round    string
	kind     string
	amount   string
	ref      string
}

func newTx(walletID, playerID, kind, amount string) txReq {
	ext := "tx-" + randHex(6)

	return txReq{
		provider: providerA,
		ext:      ext,
		key:      providerA + ":" + ext,
		walletID: walletID,
		playerID: playerID,
		round:    "round-1",
		kind:     kind,
		amount:   amount,
	}
}

func (r txReq) body() map[string]any {
	b := map[string]any{
		"providerId":            r.provider,
		"externalTransactionId": r.ext,
		"playerId":              r.playerID,
		"walletId":              r.walletID,
		"roundId":               r.round,
		"gameId":                "fortune-chimp",
		"kind":                  r.kind,
		"money":                 money(r.amount),
	}

	if r.ref != "" {
		b["referenceExternalTransactionId"] = r.ref
	}

	return b
}

func post(t *testing.T, inst *instance, tok string, r txReq) resp {
	t.Helper()

	return do(t, "POST", inst.url+"/wagering/transactions", tok,
		map[string]string{"Idempotency-Key": r.key}, r.body())
}

func postA(t *testing.T, inst *instance, r txReq) resp {
	t.Helper()
	return post(t, inst, tokProviderA(t), r)
}

func walletBalance(t *testing.T, walletID string) string {
	t.Helper()

	r := do(t, "GET", shared[0].url+"/wallets/"+walletID, tokInternal(t), nil, nil)
	if r.status != http.StatusOK {
		t.Fatalf("get wallet: status=%d body=%s", r.status, r.raw)
	}

	return r.str("balance", "amount")
}

func ledgerCount(t *testing.T, walletID, direction string) int {
	t.Helper()

	if direction == "" {
		return count(t, env.pool, `SELECT count(*) FROM ledger_entries WHERE wallet_id = $1`, walletID)
	}

	return count(t, env.pool,
		`SELECT count(*) FROM ledger_entries WHERE wallet_id = $1 AND direction = $2`,
		walletID, direction)
}

// assertReconciled confere o saldo armazenado contra créditos menos débitos
// do ledger, por SQL e pelo endpoint de reconciliação.
func assertReconciled(t *testing.T, walletID string) {
	t.Helper()

	var stored, calculated int64

	err := env.pool.QueryRow(context.Background(), `
		SELECT
			(SELECT balance FROM wallets WHERE id = $1),
			COALESCE((SELECT SUM(CASE direction WHEN 'CREDIT' THEN amount ELSE -amount END)
			          FROM ledger_entries WHERE wallet_id = $1), 0)
	`, walletID).Scan(&stored, &calculated)
	if err != nil {
		t.Fatalf("reconcile query: %v", err)
	}

	if stored != calculated {
		t.Fatalf("wallet %s: stored=%d ledger=%d", walletID, stored, calculated)
	}

	r := do(t, "POST", shared[0].url+"/wallets/"+walletID+"/reconciliation", tokInternal(t), nil, nil)
	if r.status != http.StatusOK || !r.boolean("consistent") {
		t.Fatalf("reconciliation endpoint: status=%d body=%s", r.status, r.raw)
	}
}

func eventually(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if cond() {
			return
		}

		time.Sleep(300 * time.Millisecond)
	}

	t.Fatalf("timeout waiting for: %s", msg)
}

// ---------- SQS ----------

func newQueues(prefix string) queueNames {
	return queueNames{
		Tx:     prefix + "-tx.fifo",
		DLQ:    prefix + "-tx-dlq.fifo",
		Events: prefix + "-events.fifo",
	}
}

var (
	sqsOnce   sync.Once
	sqsClient *awssqs.Client
)

func sqsCli(t *testing.T) *awssqs.Client {
	t.Helper()

	sqsOnce.Do(func() {
		cfg, err := awsconfig.LoadDefaultConfig(
			context.Background(),
			awsconfig.WithRegion("us-east-1"),
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
		)
		if err != nil {
			panic(err)
		}

		sqsClient = awssqs.NewFromConfig(cfg, func(o *awssqs.Options) {
			o.BaseEndpoint = aws.String(env.sqsEndpoint)
		})
	})

	return sqsClient
}

func queueURL(t *testing.T, name string) string {
	t.Helper()

	out, err := sqsCli(t).GetQueueUrl(context.Background(), &awssqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err != nil {
		t.Fatalf("queue url %s: %v", name, err)
	}

	return aws.ToString(out.QueueUrl)
}

func deleteQueues(q queueNames) {
	cli := awssqs.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(env.sqsEndpoint) })

	for _, name := range []string{q.Tx, q.DLQ, q.Events} {
		out, err := cli.GetQueueUrl(context.Background(), &awssqs.GetQueueUrlInput{QueueName: aws.String(name)})
		if err != nil {
			continue
		}

		_, _ = cli.DeleteQueue(context.Background(), &awssqs.DeleteQueueInput{QueueUrl: out.QueueUrl})
	}
}

func sendSQS(t *testing.T, queue string, body any, group, dedup string) {
	t.Helper()

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}

	sendRaw(t, queue, string(data), group, dedup)
}

func sendRaw(t *testing.T, queue, body, group, dedup string) {
	t.Helper()

	_, err := sqsCli(t).SendMessage(context.Background(), &awssqs.SendMessageInput{
		QueueUrl:               aws.String(queueURL(t, queue)),
		MessageBody:            aws.String(body),
		MessageGroupId:         aws.String(group),
		MessageDeduplicationId: aws.String(dedup),
	})
	if err != nil {
		t.Fatalf("send SQS message: %v", err)
	}
}

// sqsMessage monta o envelope do desafio (messageId identifica a mensagem).
func sqsMessage(messageID string, r txReq) map[string]any {
	return map[string]any{
		"messageId":  messageID,
		"type":       "WagerTransactionRequested",
		"occurredAt": time.Now().UTC().Format(time.RFC3339Nano),
		"data": map[string]any{
			"providerId":                     r.provider,
			"externalTransactionId":          r.ext,
			"idempotencyKey":                 r.key,
			"playerId":                       r.playerID,
			"walletId":                       r.walletID,
			"roundId":                        r.round,
			"gameId":                         "fortune-chimp",
			"kind":                           r.kind,
			"money":                          money(r.amount),
			"referenceExternalTransactionId": r.ref,
		},
	}
}

// drain lê e remove mensagens da fila até stop() devolver true ou o prazo
// terminar. Devolve os corpos JSON decodificados.
func drain(t *testing.T, queue string, timeout time.Duration, stop func([]map[string]any) bool) []map[string]any {
	t.Helper()

	url := queueURL(t, queue)
	deadline := time.Now().Add(timeout)

	var seen []map[string]any

	for time.Now().Before(deadline) {
		out, err := sqsCli(t).ReceiveMessage(context.Background(), &awssqs.ReceiveMessageInput{
			QueueUrl:            aws.String(url),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     2,
			VisibilityTimeout:   30,
		})
		if err != nil {
			t.Fatalf("receive %s: %v", queue, err)
		}

		for _, m := range out.Messages {
			var body map[string]any
			_ = json.Unmarshal([]byte(aws.ToString(m.Body)), &body)

			if body == nil {
				body = map[string]any{"_raw": aws.ToString(m.Body)}
			}

			seen = append(seen, body)

			_, _ = sqsCli(t).DeleteMessage(context.Background(), &awssqs.DeleteMessageInput{
				QueueUrl:      aws.String(url),
				ReceiptHandle: m.ReceiptHandle,
			})
		}

		if stop != nil && stop(seen) {
			return seen
		}
	}

	return seen
}

func queueDepth(t *testing.T, queue string) int {
	t.Helper()

	out, err := sqsCli(t).GetQueueAttributes(context.Background(), &awssqs.GetQueueAttributesInput{
		QueueUrl: aws.String(queueURL(t, queue)),
		AttributeNames: []types.QueueAttributeName{
			types.QueueAttributeNameApproximateNumberOfMessages,
			types.QueueAttributeNameApproximateNumberOfMessagesNotVisible,
		},
	})
	if err != nil {
		t.Fatalf("queue attributes: %v", err)
	}

	total := 0

	for _, v := range out.Attributes {
		var n int
		fmt.Sscanf(strings.TrimSpace(v), "%d", &n)
		total += n
	}

	return total
}
