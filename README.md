# Wager — processamento distribuído de apostas

Serviço em Go (Uber Fx, PostgreSQL, AWS SQS, Keycloak) que processa operações
financeiras de provedores de jogos (`BET`, `WIN`, `LOSS`, `REFUND`,
`ROLLBACK`) sobre carteiras de jogadores, com garantias que se mantêm com
várias instâncias e falhas entre as etapas. As decisões técnicas, limitações e
interpretações estão em [ARCHITECTURE.md](ARCHITECTURE.md).

## Pré-requisitos

- Docker com Docker Compose v2
- Go 1.25 (o mesmo declarado em `go.mod` e no `Dockerfile`) para rodar os
  testes fora do Docker
- `make` (opcional; os comandos equivalentes estão abaixo)

## Início rápido

```bash
docker compose up --build
```

Sobe PostgreSQL, aplica as migrations (serviço `migrate`), provisiona o
LocalStack (filas), importa o realm do Keycloak e inicia a API em
`http://localhost:8080`. O Keycloak leva de 30 a 60 segundos para ficar pronto;
a API aguarda o IdP antes de aceitar requisições.

Para várias instâncias independentes (portas 8080 a 8089):

```bash
docker compose up --build --scale app=3
```

Se você já tinha um volume do PostgreSQL de versões anteriores, recrie-o:
`docker compose down -v`.

## Variáveis de ambiente

Copie `.env.example` para `.env` para rodar a API fora do Docker
(`set -a; . ./.env; set +a; go run ./cmd/api`). Os valores são locais e de
exemplo.

| Variável | Padrão | Uso |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Endereço de escuta da API |
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME` | `localhost`, `5432`, `wager`, `wager`, `wager` | PostgreSQL |
| `SQS_ENDPOINT` | `http://localhost:4566` | Endpoint do SQS (LocalStack) |
| `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | `us-east-1`, `test`, `test` | Credenciais do SQS local |
| `SQS_QUEUE_NAME`, `SQS_DLQ_NAME`, `SQS_EVENT_QUEUE_NAME` | `wager-transactions.fifo`, `wager-transactions-dlq.fifo`, `wager-events.fifo` | Filas |
| `AUTH_ISSUER_URL` | `http://localhost:8081/realms/wager` | `iss` esperado nos tokens |
| `AUTH_DISCOVERY_URL` | vazio (= issuer) | URL de descoberta/JWKS quando difere do issuer (Docker) |
| `AUTH_AUDIENCE` | `wager-api` | `aud` exigido |
| `PENDING_REFERENCE_TTL` | `5m` | Prazo de espera de uma reversão pela referência |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

## Filas (SQS)

As filas são provisionadas de duas formas equivalentes:

- `localstack/init-queues.sh` roda quando o LocalStack fica pronto e cria
  `wager-transactions-dlq.fifo`, `wager-transactions.fifo` (com redrive para a
  DLQ após 5 recebimentos, visibility timeout de 30 s) e `wager-events.fifo`.
- A API também garante a existência das filas ao iniciar (cria se faltar).

Para inspecionar: `docker compose exec localstack awslocal sqs list-queues`.

## Migrations

As migrations ficam em `migrations/` e as reversões em `migrations/down/`. O
script `scripts/migrate.sh` controla as versões aplicadas na tabela
`schema_migrations`.

```bash
make migrate                       # aplica as pendentes
make migrate-down                  # reverte a última
make migrate-down N=3              # reverte as 3 últimas
# sem make:
docker compose run --rm migrate
docker compose run --rm migrate sh /scripts/migrate.sh down 1
```

## Autenticação e identidades de teste

O realm `wager` é importado automaticamente
(`keycloak/realm-wager.json`). Todos usam `client_credentials`:

| client_id | client_secret | Papel | Observação |
|---|---|---|---|
| `provider-a` | `provider-a-secret` | `provider` | claim `providerId = provider-a` |
| `provider-b` | `provider-b-secret` | `provider` | claim `providerId = provider-b` |
| `wallet-service` | `wallet-service-secret` | `internal` | operações de carteira |
| `provider-expiring` | `provider-expiring-secret` | `provider` | token de 3 s (testes de expiração) |

Console do Keycloak: `http://localhost:8081` (admin / admin).

```bash
token() {  # uso: token <client_id> <client_secret>
  curl -s -X POST http://localhost:8081/realms/wager/protocol/openid-connect/token \
    -d grant_type=client_credentials -d client_id="$1" -d client_secret="$2" \
    | sed -E 's/.*"access_token":"([^"]+)".*/\1/'
}
INTERNAL=$(token wallet-service wallet-service-secret)
PROVIDER=$(token provider-a provider-a-secret)
```

Regras: o `providerId` vem do token e o do corpo deve coincidir (senão `403`);
provedores só consultam as próprias transações; `/wallets*` só para o papel
`internal`; `/health/*` e `/metrics` são públicos.

## Exemplos de chamadas

```bash
PLAYER=0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1

# 1) Abrir carteira (serviço interno)
curl -s -X POST localhost:8080/wallets -H "Authorization: Bearer $INTERNAL" \
  -H 'Content-Type: application/json' \
  -d "{\"playerId\":\"$PLAYER\",\"initialBalance\":{\"amount\":\"1000.00\",\"currency\":\"BRL\"}}"
# -> {"id":"<walletId>","playerId":"...","balance":{"amount":"1000.00","currency":"BRL"},"version":1}
WALLET=<walletId da resposta>

# 2) Aposta (provedor)
curl -s -X POST localhost:8080/wagering/transactions \
  -H "Authorization: Bearer $PROVIDER" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: provider-a:transaction-123' \
  -d "{\"providerId\":\"provider-a\",\"externalTransactionId\":\"transaction-123\",
       \"playerId\":\"$PLAYER\",\"walletId\":\"$WALLET\",\"roundId\":\"round-987\",
       \"gameId\":\"fortune-chimp\",\"kind\":\"BET\",
       \"money\":{\"amount\":\"25.00\",\"currency\":\"BRL\"}}"
# -> 201 {"transactionId":"...","status":"PROCESSED","balance":{"amount":"975.00",...},"idempotentReplay":false}
# Repetir a mesma chamada -> 200 com "idempotentReplay": true e o mesmo saldo.

# 3) Estorno (acrescente referenceExternalTransactionId)
#    "kind":"REFUND", "referenceExternalTransactionId":"transaction-123"

# 4) Consultas
curl -s localhost:8080/wallets/$WALLET -H "Authorization: Bearer $INTERNAL"
curl -s "localhost:8080/wallets/$WALLET/ledger?limit=50" -H "Authorization: Bearer $INTERNAL"
curl -s localhost:8080/providers/provider-a/wagering/transactions/transaction-123 \
  -H "Authorization: Bearer $PROVIDER"

# 5) Reconciliação (não altera o saldo)
curl -s -X POST localhost:8080/wallets/$WALLET/reconciliation -H "Authorization: Bearer $INTERNAL"

# 6) Saúde e métricas
curl -s localhost:8080/health/live
curl -s localhost:8080/health/ready
curl -s localhost:8080/metrics | grep '^wager_'
```

Enviar a mesma operação pelo SQS (mensagem para `wager-transactions.fifo`):

```bash
docker compose exec localstack awslocal sqs send-message \
  --queue-url http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wager-transactions.fifo \
  --message-group-id "$WALLET" --message-deduplication-id msg-123 \
  --message-body '{"messageId":"msg-123","type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00.000Z","data":{"providerId":"provider-a","externalTransactionId":"transaction-124","idempotencyKey":"provider-a:transaction-124","playerId":"'$PLAYER'","walletId":"'$WALLET'","roundId":"round-987","gameId":"fortune-chimp","kind":"BET","money":{"amount":"10.00","currency":"BRL"}}}'
```

Códigos HTTP e `failureCode`: veja [ARCHITECTURE.md](ARCHITECTURE.md#contrato-http).

## Testes

```bash
go test ./...            # unitários (não exigem infraestrutura)
go test -race ./...
go vet ./...
gofmt -l .               # deve listar nada
```

### Integração, múltiplas instâncias e falhas

Sem `make` (por exemplo, PowerShell no Windows), use os comandos diretos:

```powershell
docker compose up -d postgres localstack keycloak   # aguarde ~1 min pelo Keycloak
go vet -tags=integration ./...
go test -tags=integration -count=1 -timeout=20m ./test/integration/...
```

`-race` exige CGO e um compilador C; se não tiver, omita-o. No Windows o
SIGTERM não existe, então os processos de teste são encerrados à força.

Os testes de integração usam PostgreSQL, LocalStack e Keycloak reais, sem
mocks, e ficam atrás da build tag `integration`.

```bash
make up                  # docker compose up -d postgres localstack keycloak
                         # (aguarde o Keycloak ficar pronto: ~1 min)
make integration         # go test -tags=integration -race -count=1 -timeout=20m ./test/integration/...
make integration-short   # pula os testes que esperam visibility timeout/lease (30 s)
```

Cada execução cria bancos e filas próprios (prefixo aleatório) e os remove no
final. O harness compila o binário e inicia **3 processos independentes** (cada
um com conexões e memória próprias) usados pela maior parte dos cenários:

| Cenário | Arquivo |
|---|---|
| 2 apostas de 80.00 sobre 100.00, 50 requisições iguais em paralelo, carteiras distintas, 40 apostas na mesma carteira | `concurrency_test.go` |
| Conflitos de idempotência, replay com saldo original, valores inválidos, LOSS, abertura | `idempotency_test.go` |
| Sem credencial, inválida, expirada, papéis, isolamento entre provedores | `auth_test.go` |
| REFUND antes da BET, dupla reversão, reversões concorrentes, saldo insuficiente, expiração | `reversal_test.go` |
| Inbox, HTTP+SQS cruzados, rejeição de negócio, DLQ, **crash depois do commit** | `sqs_test.go` |
| Contrato dos eventos, publishers concorrentes, **crash entre publicar e marcar** | `outbox_test.go` |
| **Reinício** dos processos (idempotência e pendências) | `restart_test.go` |
| Constraints do banco (ledger imutável, saldo, unicidades) | `constraints_test.go` |
| Composição Fx: início, readiness e encerramento com liberação de recursos | `fx_test.go` |

Simulação de falhas: com `FAULT_EXIT_AT=sqs_after_commit` ou
`FAULT_EXIT_AT=outbox_after_publish` o processo encerra abruptamente no ponto
indicado (`internal/fault`). Usado apenas pelos testes; não defina em produção.

O grafo do Fx também é validado sem infraestrutura por `internal/app/app_test.go`.

O teste do repositório de carteiras (`internal/infrastructure/postgres`) também está
atrás da tag `integration` e exige PostgreSQL com as migrations:
`docker compose up -d postgres migrate` e depois
`go test -tags=integration ./internal/infrastructure/postgres/`.

## Estrutura

```
cmd/api                       ponto de entrada
internal/app                  composição Fx (fx.Options)
internal/domain               Money, Wallet, WagerTransaction, LedgerEntry (sem I/O)
internal/application          hash canônico, erros, contratos
internal/auth                 validação de token e autorização por papel
internal/http                 handlers, middlewares, health, métricas
internal/infrastructure       postgres, sqs, outbox, pendingreference
internal/logging, metrics     logs JSON e Prometheus
migrations, migrations/down   schema versionado e reversões
keycloak, localstack          provisionamento do IdP e das filas
test/integration              testes de integração (tag integration)
```
