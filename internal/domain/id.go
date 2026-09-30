package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// ID é um UUID em texto canônico (minúsculo). O valor zero ("") é inválido.
type ID string

// NewID gera um UUIDv7: 48 bits de timestamp em ms + aleatório.
// Ser "ordenável por tempo" ajuda os índices do PostgreSQL.
func NewID() ID { return newIDAt(time.Now()) }

func newIDAt(t time.Time) ID {
	var b [16]byte
	ms := uint64(t.UnixMilli())
	for i := 0; i < 6; i++ {
		b[i] = byte(ms >> (8 * (5 - i)))
	}
	if _, err := rand.Read(b[6:]); err != nil {
		// Sem entropia do SO o processo não é confiável. Não é regra de negócio.
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x70 // versão 7
	b[8] = (b[8] & 0x3f) | 0x80 // variante RFC 4122
	return formatUUID(b)
}

func formatUUID(b [16]byte) ID {
	h := hex.EncodeToString(b[:])
	return ID(h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32])
}

// ParseID valida e normaliza (minúsculas) um UUID.
func ParseID(s string) (ID, error) {
	s = strings.ToLower(s)
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return "", fmt.Errorf("%w: %q", ErrInvalidID, s)
	}
	if _, err := hex.DecodeString(strings.ReplaceAll(s, "-", "")); err != nil {
		return "", fmt.Errorf("%w: %q", ErrInvalidID, s)
	}
	return ID(s), nil
}

func (id ID) IsZero() bool   { return id == "" }
func (id ID) String() string { return string(id) }
