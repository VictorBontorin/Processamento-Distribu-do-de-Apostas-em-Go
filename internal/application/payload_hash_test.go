package application

import (
	"testing"
	"time"

	"wager/internal/domain"
)

func buildTx(t *testing.T, mutate func(*txInput)) *domain.WagerTransaction {
	t.Helper()

	in := txInput{
		id:       domain.NewID(),
		ext:      "transaction-123",
		provider: "provider-a",
		key:      "provider-a:transaction-123",
		wallet:   domain.NewID(),
		player:   domain.NewID(),
		round:    "round-987",
		game:     "fortune-chimp",
		kind:     domain.TransactionBet,
		amount:   "25.00",
		now:      time.Now(),
	}

	if mutate != nil {
		mutate(&in)
	}

	money, err := domain.ParseMoney(in.amount, "BRL")
	if err != nil {
		t.Fatalf("money: %v", err)
	}

	tx, err := domain.NewExternalTransaction(
		in.id, in.ext, in.provider, in.key, "",
		in.wallet, in.player, in.round, in.game,
		in.kind, money, in.ref, in.now,
	)
	if err != nil {
		t.Fatalf("new transaction: %v", err)
	}

	return tx
}

type txInput struct {
	id       domain.ID
	ext      string
	provider string
	key      string
	wallet   domain.ID
	player   domain.ID
	round    string
	game     string
	kind     domain.TransactionKind
	amount   string
	ref      string
	now      time.Time
}

func hashOf(t *testing.T, tx *domain.WagerTransaction) string {
	t.Helper()

	h, err := CanonicalTransactionHash(tx)
	if err != nil {
		t.Fatal(err)
	}

	return h
}

func TestCanonicalHashIsDeterministicAndIgnoresTransportMetadata(t *testing.T) {
	wallet, player := domain.NewID(), domain.NewID()

	a := buildTx(t, func(in *txInput) { in.wallet, in.player = wallet, player })

	// Outro id interno, outra chave de idempotência e outro instante: o
	// conteúdo de negócio é o mesmo, então o hash também.
	b := buildTx(t, func(in *txInput) {
		in.wallet, in.player = wallet, player
		in.key = "another-header-value"
		in.now = time.Now().Add(time.Hour)
	})

	if hashOf(t, a) != hashOf(t, b) {
		t.Fatal("hash must not depend on id, Idempotency-Key or timestamps")
	}

	if hashOf(t, a) != hashOf(t, a) {
		t.Fatal("hash must be deterministic")
	}
}

func TestCanonicalHashChangesWithAnyBusinessField(t *testing.T) {
	wallet, player := domain.NewID(), domain.NewID()
	base := func(in *txInput) { in.wallet, in.player = wallet, player }
	reference := hashOf(t, buildTx(t, base))

	variants := map[string]func(*txInput){
		"amount":   func(in *txInput) { in.amount = "25.01" },
		"provider": func(in *txInput) { in.provider = "provider-b" },
		"external": func(in *txInput) { in.ext = "transaction-124" },
		"round":    func(in *txInput) { in.round = "round-1" },
		"game":     func(in *txInput) { in.game = "other-game" },
		"kind":     func(in *txInput) { in.kind = domain.TransactionWin },
		"wallet":   func(in *txInput) { in.wallet = domain.NewID() },
		"player":   func(in *txInput) { in.player = domain.NewID() },
	}

	for name, change := range variants {
		tx := buildTx(t, func(in *txInput) {
			base(in)
			change(in)
		})

		if hashOf(t, tx) == reference {
			t.Fatalf("hash did not change when %s changed", name)
		}
	}
}

func TestCanonicalHashIncludesReferenceForReversals(t *testing.T) {
	wallet, player := domain.NewID(), domain.NewID()

	mk := func(ref string) *domain.WagerTransaction {
		return buildTx(t, func(in *txInput) {
			in.wallet, in.player = wallet, player
			in.kind = domain.TransactionRefund
			in.ref = ref
		})
	}

	if hashOf(t, mk("bet-1")) == hashOf(t, mk("bet-2")) {
		t.Fatal("hash must depend on referenceExternalTransactionId")
	}
}

// HTTP e SQS montam a operação por caminhos diferentes, mas o hash é o mesmo.
func TestWithPayloadHashSetsTheCanonicalHash(t *testing.T) {
	tx := buildTx(t, nil)

	hashed, err := WithPayloadHash(tx)
	if err != nil {
		t.Fatal(err)
	}

	if hashed.PayloadHash() == "" || hashed.PayloadHash() != hashOf(t, tx) {
		t.Fatalf("payload hash = %q", hashed.PayloadHash())
	}

	if hashed.ID() != tx.ID() || hashed.IdempotencyKey() != tx.IdempotencyKey() {
		t.Fatal("WithPayloadHash must preserve identity and idempotency key")
	}
}
