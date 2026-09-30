package http

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// isTransient identifica falhas temporárias de infraestrutura (banco
// indisponível, timeout, deadlock, falha de serialização): o cliente pode
// repetir a mesma requisição com a mesma Idempotency-Key.
func isTransient(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		for _, prefix := range []string{"08", "53", "57", "40"} {
			if strings.HasPrefix(pgErr.Code, prefix) {
				return true
			}
		}
	}

	// Falhas de conexão do pgx antes de chegar ao servidor.
	var connErr *pgconn.ConnectError
	return errors.As(err, &connErr)
}

var defaultErrorCodes = map[int]string{
	http.StatusBadRequest:          "INVALID_REQUEST",
	http.StatusUnauthorized:        "UNAUTHENTICATED",
	http.StatusForbidden:           "FORBIDDEN",
	http.StatusNotFound:            "NOT_FOUND",
	http.StatusConflict:            "CONFLICT",
	http.StatusInternalServerError: "INTERNAL_ERROR",
	http.StatusServiceUnavailable:  "TEMPORARILY_UNAVAILABLE",
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSONErrorCode(w, status, defaultErrorCodes[status], message)
}

func writeJSONErrorCode(
	w http.ResponseWriter,
	status int,
	code string,
	message string,
) {
	writeJSON(w, status, map[string]string{
		"error": message,
		"code":  code,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(body)
}
