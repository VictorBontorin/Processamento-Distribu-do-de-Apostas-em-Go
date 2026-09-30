# Arquitetura e decisões técnicas

Este documento registra as decisões, as interpretações adotadas e as limitações
da solução. Para executar o projeto, veja o [README](README.md).

## Visão geral

```
Provedor ──HTTP──► API (N instâncias, stateless) ──► PostgreSQL (fonte da verdade)
Provedor ──SQS───► Consumer (em cada instância)  ──►   wallets · wager_transactions
                                                        ledger_entries · inbox_messages
                   Publisher (em cada instância) ◄──    outbox_events
                        │
                        └──► SQS wager-events.fifo (eventos de integração)
Keycloak (OIDC) ◄── validação de JWT (JWKS) na API
```

Nenhum estado crítico vive em memória. Toda garantia (unicidade, saldo
não negativo, imutabilidade do ledger, idempotência) é imposta pelo PostgreSQL;
travas e deduplicações da aplicação são otimizações sobre essas barreiras.

## Dinheiro

- **Representação:** `Money` é um value object imutável com `int64` em
  **unidades mínimas** (centavos) mais o código de moeda ISO 4217. Nunca há
  `float32`/`float64` no parsing, no cálculo, na serialização nem na
  persistência.
- **Limites:** faixa de `int64` em centavos (±92 quatrilhões de unidades
  principais). Overflow é tratado no parsing, na soma, na subtração e na
  negação (`MinInt64` incluso).
- **Contrato externo:** `{"amount":"25.00","currency":"BRL"}`, escala fixa de
  duas casas. São rejeitados: vazio, `NaN`, `Infinity`, notação científica,
  escala excedente e valores negativos nas entradas financeiras. Nenhuma
  entrada inválida é arredondada. `amount` como número JSON é recusado pelo
  decoder. Não há formas equivalentes aceitas, portanto não há normalização de
  valor antes do hash.
- **Aritmética:** exige moedas compatíveis (erro de domínio caso contrário).
  Negativos existem apenas em diferenças internas (por exemplo, na
  reconciliação), nunca no saldo.
- **Persistência:** `BIGINT` em unidades mínimas (colunas `amount`,
  `balance`, `balance_before`, `balance_after`) e `currency` em colunas
  próprias.
- **Moedas:** o tipo carrega a moeda e há testes de incompatibilidade. A carteira
  é identificada por `(player_id, currency)`.

## Acesso ao banco e transações

- **Biblioteca:** `pgx/v5` com SQL explícito (`pgxpool`). Nenhum ORM.
  Transações, locks e constraints aparecem no SQL e no schema.
- **Delimitação da transação:** um `WagerUnitOfWork` abre a transação
  (`READ COMMITTED`) e entrega aos repositórios (`WalletTxRepository`,
  `WagerTransactionTxRepository`, `LedgerTxRepository`, `OutboxTxRepository`,
  `InboxTxRepository`) o mesmo `pgx.Tx`. Saldo, transação, ledger, snapshot
  do resultado, inbox e outbox são confirmados **juntos** ou não são.
  Eventos só são publicados por um worker separado, depois do commit.
- **Domínio independente:** `internal/domain` não importa Fx, HTTP, SQS nem
  pgx. Construtores validam; a reidratação é separada da criação e não reaplica
  movimentos nem emite eventos. Erros de domínio são classificáveis com
  `errors.Is/As`; não há `panic` para regras de negócio.
- **Leituras** (`ReadStore`): ledger paginado, consultas e reconciliação, sem
  travar escritores.

### Migrations

Versionadas em `migrations/NNN_nome.sql`, com reversão em
`migrations/down/NNN_nome.down.sql`. `scripts/migrate.sh` aplica (`up`) e
reverte (`down [N]`) registrando as versões em `schema_migrations`; no Compose
roda como o serviço `migrate`, antes da API.

| Migration | Conteúdo |
|---|---|
| 001 | `wallets`, `wager_transactions`, `ledger_entries`, `outbox_events`, constraints |
| 002 | `inbox_messages` |
| 003 | hash do payload, snapshot do resultado |
| 004 | ledger imutável (triggers contra UPDATE, DELETE e TRUNCATE) |
| 005 | colunas de retry/lease da outbox |
| 006 | colunas de referência pendente |
| 007 | no máximo uma reversão processada por referência |
| 008 | OPENING × externas (CHECK) e um único OPENING por carteira |
| 009 | `seq` no ledger (paginação estável) |

## Concorrência

**Estratégia: lock pessimista por carteira + atualização versionada + constraints.**

1. A transação trava **apenas a linha da carteira** com
   `SELECT ... FOR UPDATE`. Carteiras diferentes usam linhas diferentes e
   avançam em paralelo; não há lock global nem advisory lock global.
2. O saldo é alterado no agregado `Wallet` (débito rejeita saldo insuficiente) e
   persistido com `UPDATE ... SET balance, version = version + 1`, dentro da
   mesma transação. Como a linha está travada, não há *lost update*.
3. Barreiras no banco, independentes da aplicação: `CHECK (balance >= 0)`,
   `UNIQUE (wallet_id, transaction_id)` no ledger, triggers de imutabilidade.
4. Reversões travam também a linha da **transação referenciada** antes da
   carteira (ordem fixa: referência → carteira), evitando deadlock entre
   reversões concorrentes.

Por que pessimista: a disputa por uma mesma carteira é o caso de uso
crítico (duas apostas de 80 sobre 100). O lock serializa os escritores sem
retries e mantém o resultado determinístico: uma `PROCESSED`, uma
`REJECTED/INSUFFICIENT_FUNDS`, saldo 20.00 e um único débito. Conflitos
(`40001`, `40P01`, `23505`) são contabilizados na métrica
`wager_concurrency_conflicts_total`.

## Idempotência

- **Persistente:** chave em `wager_transactions(provider_id, idempotency_key)`
  e `(provider_id, external_transaction_id)`, ambas `UNIQUE`. Sobrevive ao
  reinício de qualquer processo (estado só no PostgreSQL).
- **Criação atômica:** `INSERT ... ON CONFLICT DO NOTHING` (`CreateIfAbsent`).
  Requisições simultâneas idênticas esperam a primeira confirmar e recebem o
  resultado dela como replay.
- **Hash do payload:** SHA-256 (hex) de um JSON canônico — `map[string]string`
  serializado com chaves em ordem alfabética — com os campos `providerId`,
  `externalTransactionId`, `playerId`, `walletId`, `roundId`, `gameId`, `kind`,
  `amount`, `currency`, `referenceExternalTransactionId`. **Excluídos:**
  `Idempotency-Key`, `messageId`, `occurredAt` e qualquer metadado de
  transporte. `kind` é normalizado para maiúsculas sem espaços. O cálculo é o
  mesmo para HTTP e SQS (`application.CanonicalTransactionHash`), o que torna as
  duas origens equivalentes.
- **Resultados:**
  - mesma chave e mesmo hash → resultado persistido, `idempotentReplay: true`
    (`200`, ou o mesmo status de rejeição);
  - mesma chave com hash diferente → `409 IDEMPOTENCY_CONFLICT`;
  - mesma operação `(providerId, externalTransactionId)` com outra chave →
    `409 EXTERNAL_TRANSACTION_CONFLICT`.
- **Saldo do replay:** o saldo observado no processamento original é gravado
  em `result_balance`/`result_currency` na mesma transação e devolvido nos
  replays, mesmo que a carteira já tenha outras movimentações.
- O servidor nunca substitui a chave recebida por outra calculada; o header é
  obrigatório (`400` sem ele).

## Máquina de estados da transação

```
PENDING ──► PROCESSED            (terminal)
   │   ├──► REJECTED             (terminal; regra de negócio)
   │   ├──► FAILED               (terminal; falha permanente registrada)
   │   └──► PENDING_REFERENCE ──► PROCESSED | REJECTED | FAILED
```

O domínio valida cada transição; estados terminais não mudam. Um replay lê o
resultado persistido e não reaplica nada. `OPENING` (abertura interna) nasce
`PROCESSED`.

**Falha transitória × permanente.** Falhas transitórias (banco indisponível,
timeout, deadlock, SQS fora) **não** são persistidas: a transação SQL é
revertida, o HTTP responde `503` com `Retry-After` e o SQS reagenda a mensagem
com backoff; a repetição usa a mesma chave. Falhas permanentes são: rejeição de
negócio (`REJECTED`, com `failureCode`) e, no SQS, mensagem inválida ou conflito
de conteúdo (DLQ). Todo `PENDING_REFERENCE` confirmado é retomado por qualquer
instância (worker).

## Operações e reversões

| Tipo | Movimento | Regras |
|---|---|---|
| `BET` | débito | valor > 0, saldo suficiente |
| `WIN` | crédito | valor > 0 |
| `LOSS` | nenhum | valor `0.00`; sem ledger, sem alterar a versão |
| `REFUND` | crédito | devolve integralmente uma `BET` processada |
| `ROLLBACK` | contrário ao original | desfaz integralmente `BET` (crédito), `WIN` (débito) ou `REFUND` (débito) |

- `OPENING` é rejeitado em HTTP e SQS (`400` / DLQ).
- Reversão exige `referenceExternalTransactionId`, resolvido por
  `(providerId, referenceExternalTransactionId)`, e concordância de provedor,
  jogador, carteira, moeda, rodada e **valor** (sem reversão parcial).
- **Uma referência recebe no máximo uma reversão processada, de qualquer
  tipo.** Isso cobre "duas reversões do mesmo tipo" e também combinações
  `REFUND`+`ROLLBACK` sobre a mesma aposta, impedindo devolver o mesmo débito
  duas vezes. A regra é imposta por consulta sob lock da referência **e** pelo
  índice único parcial `wager_transactions_single_reversal_uq`
  (`reference_transaction_id` onde `PROCESSED` e `REFUND/ROLLBACK`). A segunda
  reversão é rejeitada com `DUPLICATE_REVERSAL`. `ROLLBACK` de um `REFUND` é
  permitido (a referência do rollback é o refund).
- **Reversão sem saldo** (por exemplo, `ROLLBACK` de um `WIN` já gasto) é
  rejeitada e auditável com `REVERSAL_INSUFFICIENT_FUNDS`, distinto de
  `INSUFFICIENT_FUNDS` (aposta sem saldo).

### Referências ainda indisponíveis

- Reversão cuja referência não existe (ou existe, mas ainda não terminou) é
  gravada como `PENDING_REFERENCE` (`202`), com `reference_expires_at`,
  `reference_attempts` e `reference_next_attempt_at`, e o evento
  `WagerTransactionPendingReference`.
- Um worker (em toda instância) reivindica pendências vencidas com
  `FOR UPDATE SKIP LOCKED` e tenta de novo com **backoff exponencial**
  (1 s, 2 s, 4 s … até 60 s). Sobrevive a reinícios (estado no banco).
- **Prazo (TTL): 5 minutos** (`PENDING_REFERENCE_TTL`), em vez de número máximo de
  tentativas. Esgotado:
  - a referência nunca apareceu → `REJECTED` com `REFERENCE_NOT_FOUND`;
  - a referência existe, mas seguiu pendente → `REJECTED` com
    `REFERENCE_NOT_PROCESSED`;
  - em ambos há evento `WagerTransactionRejected`.
- Referência que existe e terminou **sem sucesso** (`REJECTED`/`FAILED`) → rejeição
  imediata com `REFERENCE_NOT_PROCESSED` (nunca haverá movimento a reverter).

### `failureCode` estáveis

| Código | Origem | Corrigível? |
|---|---|---|
| `INSUFFICIENT_FUNDS` | aposta sem saldo | resultado definitivo para aquela operação |
| `REVERSAL_INSUFFICIENT_FUNDS` | reversão que debitaria além do saldo | definitivo |
| `DUPLICATE_REVERSAL` | referência já revertida | definitivo |
| `INVALID_REVERSAL` | provedor/jogador/carteira/moeda/rodada/valor divergentes, ou combinação de tipos inválida | o provedor deve corrigir e reenviar com **novo** `externalTransactionId` |
| `REFERENCE_NOT_FOUND` | TTL esgotado sem a referência | definitivo |
| `REFERENCE_NOT_PROCESSED` | referência terminou sem sucesso ou seguiu pendente | definitivo |
| `WALLET_ERROR` | erro inesperado de carteira (ver limitações) | — |

Erros de entrada (JSON/valor/tipo inválidos, `OPENING`) não geram transação: são
`400` e corrigíveis com a mesma chave.

## Inbox e outbox

- **Inbox** (`inbox_messages`): PK `(consumer_name, message_id)`, com hash do
  envelope, `received_at` e `completed_at`. O registro da inbox, a mudança de
  domínio, o ledger e os eventos compartilham **uma** transação SQL. Reentrega
  com o mesmo hash e já concluída é descartada; hash diferente para o mesmo
  `messageId` é falha permanente (DLQ).
- **Outbox** (`outbox_events`): `id` estável (eventId), agregado, tipo, payload
  (snapshot JSON imutável), ocorrência, tentativas, próximo envio, `locked_until`
  e `published_at`. Gravada na mesma transação do fato.
- **Publisher** (em toda instância): `FOR UPDATE SKIP LOCKED` reivindica um
  evento por vez com *lease* de 30 s; publica; marca `published_at`. Se a
  instância morrer depois de publicar e antes de marcar (ou antes de publicar),
  o lease expira e **outra instância assume**, republicando o **mesmo eventId**
  (que também é o `MessageDeduplicationId`). Falha de publicação → backoff
  exponencial (1 s … 5 min) persistido em `next_attempt_at`. Garantia:
  **at-least-once**; consumidores devem deduplicar por `eventId`.

### Eventos de saída

Fila `wager-events.fifo` (`MessageGroupId` = `aggregateId`, `MessageDeduplicationId`
= `eventId`). Envelope JSON:

```json
{"eventId":"…","eventType":"WalletBalanceChanged","aggregateId":"…",
 "correlationId":"…","causationId":"…","occurredAt":"RFC3339 UTC",
 "version":1,"data":{…}}
```

| eventType | Gatilho | aggregateId |
|---|---|---|
| `WagerTransactionProcessed` | conclusão bem-sucedida, inclui `LOSS` e `OPENING` | transação |
| `WagerTransactionRejected` | rejeição definitiva | transação |
| `WagerTransactionPendingReference` | registro de espera pela referência | transação |
| `WalletBalanceChanged` | alteração efetiva de saldo (inclui abertura com saldo) | carteira |

`WalletBalanceChanged.data`: `walletId`, `transactionId`, `direction`, `money`,
`balanceBefore`, `balanceAfter`, `walletVersion`. Dinheiro sempre em strings
decimais; tempos em UTC RFC 3339. `version` é a versão do esquema do evento
(definida no construtor do evento); o `correlationId` vem do cabeçalho
`X-Correlation-Id` (ou é gerado) e do contexto do consumidor SQS.

## Consumidor SQS

- **Filas:** `wager-transactions.fifo` (principal) e
  `wager-transactions-dlq.fifo`, com *redrive* (`maxReceiveCount = 5`) e
  *visibility timeout* de 30 s.
- **`MessageGroupId` / `MessageDeduplicationId`** (produtores): grupo =
  `walletId` (ordem por carteira); deduplicação = `messageId`. A correção
  financeira **não** depende disso: a serialização real está no lock da carteira
  e a duplicata é descartada pela inbox mesmo que o SQS entregue cópias.
- **Identidade da mensagem:** `messageId` do envelope, com hash verificado nas
  reentregas. Chave de idempotência: `data.idempotencyKey`.
- **Remoção:** só depois do commit. Sucesso e rejeição de negócio confirmada
  são terminais (removem a mensagem).
- **Erros permanentes** (envelope/JSON inválido, tipo desconhecido, campos
  inválidos, `OPENING`, conflito de conteúdo/hash) → enviados **direto à DLQ**
  (com o motivo em `failureReason`) e removidos da fila principal.
- **Erros transitórios** → mensagem mantida, novo *visibility timeout* com backoff
  exponencial (2 s, 4 s … 64 s); após 5 recebimentos, o redrive do SQS a move
  para a DLQ.
- **SIGTERM:** para de buscar mensagens, conclui a em andamento dentro do prazo
  de parada do Fx; vencido o prazo, cancela (a transação SQL é revertida) e
  **libera a visibilidade** (`0 s`) para reentrega segura.
- **Concorrência HTTP × SQS:** ambas as entradas usam o mesmo caso de uso
  (`processExternalTransaction`) e as mesmas barreiras de idempotência; testes
  cruzam os dois caminhos para a mesma operação, em ambas as ordens.

## Contrato HTTP

| Situação | Status | Corpo |
|---|---|---|
| Processada (nova) | `201` | `transactionId`, `status: PROCESSED`, `balance`, `idempotentReplay: false` |
| Replay de processada | `200` | idem, `idempotentReplay: true`, saldo original |
| Aceita, aguardando referência | `202` | `status: PENDING_REFERENCE`, sem `balance` |
| Rejeição de negócio (inclui replay) | `422` | `status: REJECTED`, `failureCode`, `balance` |
| Falha permanente registrada | `500` | `status: FAILED`, `failureCode` |
| Entrada inválida | `400` | `{"error","code":"INVALID_REQUEST"}` |
| Sem credencial / inválida / expirada | `401` | `code: UNAUTHENTICATED` + `WWW-Authenticate` |
| Sem permissão / provedor diferente | `403` | `code: FORBIDDEN` |
| Conflito de idempotência | `409` | `code: IDEMPOTENCY_CONFLICT` ou `EXTERNAL_TRANSACTION_CONFLICT` |
| Carteira duplicada (jogador+moeda) | `409` | `code: CONFLICT` |
| Indisponibilidade transitória | `503` | `code: TEMPORARILY_UNAVAILABLE` + `Retry-After` |

Endpoints: `POST /wallets`, `GET /wallets/{id}`, `GET /wallets/{id}/ledger`
(cursor opaco `v1:<seq>` em base64url, ordenado por `seq`, `limit` 1–200),
`POST /wallets/{id}/reconciliation`, `POST /wagering/transactions`,
`GET /wagering/transactions/{id}`,
`GET /providers/{p}/wagering/transactions/{ext}`, `GET /health/live`,
`GET /health/ready` (PostgreSQL + SQS), `GET /metrics`.

**Reconciliação:** em `REPEATABLE READ` somente leitura, soma créditos menos
débitos do ledger (incluindo a abertura), compara com o saldo armazenado e
devolve `difference = armazenado − reconstruído`. Não altera o saldo; divergência
gera log de erro e a métrica `wager_reconciliation_divergences_total`.

**Abertura de carteira:** `POST /wallets` com saldo positivo cria, no mesmo
commit, a carteira (versão 1), a transação `OPENING` (`PROCESSED`), o lançamento
de crédito e os eventos `WagerTransactionProcessed` e `WalletBalanceChanged`.
Saldo zero não cria `OPENING`, ledger nem eventos. O schema distingue internas de
externas (`CHECK`) e permite um único `OPENING` por carteira.

## Autenticação e autorização

- **IdP:** Keycloak (OIDC), externo à aplicação, provisionado por realm importado.
  O serviço não cadastra senhas nem emite tokens.
- **Fluxo:** `client_credentials` entre serviços. A API valida o JWT em cada
  requisição: assinatura (JWKS, com cache e rotação do IdP), emissor (`iss`),
  audiência (`aud = wager-api`) e expiração. Sem credencial, inválida ou
  expirada → `401` antes de qualquer efeito.
- **Modelo de permissões (papéis do realm):**
  - `provider`: envia operações e consulta **apenas as próprias** transações. O
    `providerId` autorizado vem do claim `providerId` do token; o do corpo/caminho
    precisa coincidir (`403`). Consulta de transação de outro provedor por id →
    `404` (não revela existência). O espaço de idempotência é por provedor, então
    replays nunca cruzam provedores.
  - `internal`: operações de carteira (abrir, consultar, ledger, reconciliar) e
    leitura de qualquer transação. Provedores não acessam carteiras.
- **Mensageria:** o acesso ao SQS é controlado por credenciais e políticas do
  broker (IAM/queue policy em produção); o consumidor mantém todas as validações de
  domínio, pois não confia no produtor. No LocalStack as políticas não são
  aplicadas (ver limitações).

## Uber Fx e ciclo de vida

- `internal/app.Options()` compõe tudo com `fx.Provide`/`fx.Invoke`: configuração
  (env), pool, unit of work, repositórios, store, handlers, servidor, verificador
  OIDC, cliente SQS, consumer, publisher e worker de referências.
- **Início:** a configuração é validada nos construtores (falha rápida); o
  verificador aguarda o IdP e o cliente SQS aguarda o endpoint e garante as filas.
- **Workers:** cada um tem contexto próprio (não o do `OnStart`, que o Fx cancela
  ao terminar a inicialização), `WaitGroup` para término observável e
  cancelamento no `OnStop`.
- **Shutdown** (ordem inversa da construção): servidor HTTP deixa de aceitar
  entradas e conclui as requisições; consumer para de buscar e conclui ou libera
  a mensagem; publisher e worker terminam o ciclo corrente; **por último** o pool
  do PostgreSQL é fechado.
- O grafo é validado por `fx.ValidateApp` (teste unitário) e a composição sobe e
  encerra com infraestrutura real em `fx_test.go`.

## Observabilidade

Logs JSON (`log/slog`) com `correlationId`, `messageId`, `transactionId`,
`walletId` e `providerId` quando disponíveis; sem credenciais nem payloads
financeiros completos (chaves sensíveis são mascaradas). Métricas Prometheus em
`/metrics`: resultados por status, rejeições por código, duplicatas, retries,
DLQ, conflitos de concorrência, atraso da outbox, latência (processamento e
HTTP) e divergências de reconciliação. Health checks: `/health/live`,
`/health/ready`. Tracing OpenTelemetry e dashboards não foram implementados.

## Limitações, interpretações e trabalho não concluído

**Interpretações adotadas**
- Uma referência aceita **uma** reversão processada de qualquer tipo
  (`REFUND` e `ROLLBACK` são mutuamente exclusivos sobre a mesma aposta).
- `ROLLBACK` de `REFUND` é permitido; `REFUND` só incide sobre `BET`.
- O TTL de espera pela referência substitui o limite de tentativas.
- `REJECTED` é `422` no HTTP (inclusive em replays); `PENDING_REFERENCE` é `202`.

**Limitações conhecidas**
- **Propriedade da carteira:** o desafio não define qual provedor é "dono" de uma
  carteira; qualquer provedor autenticado pode operar sobre uma `walletId` informada
  (o isolamento é entre as *transações* dos provedores).
- **Política do broker:** o LocalStack não impõe IAM/queue policies; as credenciais
  locais são fixas (`test`/`test`). A política real deve ser definida na AWS.
- **`FAILED`:** erros inesperados de carteira revertem a transação (tratados como
  transitórios, com retry); o estado `FAILED` existe no domínio e no contrato, mas
  não é gravado por nenhum caminho hoje.
- **Escalabilidade do publisher:** publica um evento por vez por instância (com
  drenagem em laço); suficiente para o escopo, não otimizado para alto volume.
- **Ordem do ledger:** `seq` é atribuído sob o lock da carteira, o que garante a
  ordem de commit por carteira; não há ordem global entre carteiras.
- **Sem testes de carga** e sem partidas dobradas (ambos opcionais no desafio).
- Os testes de integração exigem Docker (PostgreSQL, LocalStack, Keycloak) e
  levam alguns minutos; os que dependem do visibility timeout/lease (30 s) podem
  ser pulados com `-short`.
- Injeção de falhas (`FAULT_EXIT_AT`) existe apenas para os testes de recuperação.
