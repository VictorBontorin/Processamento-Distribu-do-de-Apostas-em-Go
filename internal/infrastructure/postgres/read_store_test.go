package postgres

import (
	"errors"
	"testing"
)

func TestLedgerCursorRoundTrip(t *testing.T) {
	for _, seq := range []int64{0, 1, 42, 9_000_000_000} {
		got, err := decodeCursor(encodeCursor(seq))
		if err != nil || got != seq {
			t.Fatalf("seq=%d: got=%d err=%v", seq, got, err)
		}
	}

	if seq, err := decodeCursor(""); err != nil || seq != 0 {
		t.Fatalf("empty cursor: %d %v", seq, err)
	}
}

func TestLedgerCursorRejectsGarbage(t *testing.T) {
	for _, cursor := range []string{"!!!", "abc", "djE6LTE"} { // "djE6LTE" = "v1:-1"
		if _, err := decodeCursor(cursor); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("cursor %q: err=%v, want ErrInvalidCursor", cursor, err)
		}
	}
}
