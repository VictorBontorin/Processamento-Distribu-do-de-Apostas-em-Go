// Package fault oferece pontos de injeção de falha para testes de
// recuperação. Com FAULT_EXIT_AT=<ponto>[,<ponto>], o processo encerra
// abruptamente (sem shutdown) ao atingir o ponto. Sem a variável, não faz nada.
//
// Pontos: sqs_after_commit (depois do commit e antes de remover a mensagem)
// e outbox_after_publish (depois de publicar e antes de marcar na outbox).
package fault

import (
	"os"
	"strings"
)

func enabled(point string) bool {
	for _, p := range strings.Split(os.Getenv("FAULT_EXIT_AT"), ",") {
		if strings.TrimSpace(p) == point {
			return true
		}
	}

	return false
}

// ExitIf encerra o processo imediatamente (como um kill -9) se o ponto
// estiver habilitado.
func ExitIf(point string) {
	if enabled(point) {
		os.Exit(137)
	}
}
