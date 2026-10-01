package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"wager/internal/application"
	"wager/internal/domain"
)

// ErrWalletNotFound: a carteira informada não existe (permanente).
var ErrWalletNotFound = fmt.Errorf("%w: wallet not found", application.ErrPermanent)

// lockWallet trava a linha da carteira (FOR UPDATE).
//
// Deve ser a PRIMEIRA operação de toda transação que mexe em uma carteira.
// Motivo: inserir em wager_transactions ou ledger_entries toma, via chave
// estrangeira, um lock FOR KEY SHARE na linha da carteira. Se duas transações
// inserissem a linha da operação antes de pedir FOR UPDATE, cada uma seguraria
// KEY SHARE e esperaria pelo FOR UPDATE da outra: deadlock (40P01).
// Com a carteira travada primeiro, as transações da mesma carteira apenas
// entram em fila, sem deadlock, e carteiras diferentes seguem em paralelo.
func lockWallet(
	ctx context.Context,
	r *WagerTransactionContext,
	walletID domain.ID,
) error {
	var one int

	err := r.Wallet.tx.QueryRow(
		ctx,
		`SELECT 1 FROM wallets WHERE id = $1 FOR UPDATE`,
		walletID.String(),
	).Scan(&one)

	if errors.Is(err, pgx.ErrNoRows) {
		return ErrWalletNotFound
	}

	if err != nil {
		return fmt.Errorf("lock wallet: %w", err)
	}

	return nil
}
