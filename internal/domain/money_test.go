package domain

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestParseMoney_Valid(t *testing.T) {
	cases := map[string]int64{
		"0.00": 0, "0.01": 1, "25.00": 2500, "1000.00": 100000, "1.05": 105,
		"92233720368547758.07": math.MaxInt64,
	}
	for in, want := range cases {
		m, err := ParseMoney(in, "BRL")
		if err != nil {
			t.Fatalf("%q: unexpected error %v", in, err)
		}
		if m.Units() != want {
			t.Errorf("%q: units=%d want %d", in, m.Units(), want)
		}
		if m.Amount() != in {
			t.Errorf("%q: round trip gave %q", in, m.Amount())
		}
	}
}

func TestParseMoney_Invalid(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{"", ErrInvalidAmount},
		{"-1.00", ErrNegativeAmount},
		{"NaN", ErrInvalidAmount},
		{"Infinity", ErrInvalidAmount},
		{"1e3", ErrInvalidAmount},
		{"1.0e2", ErrInvalidAmount},
		{"25", ErrInvalidAmount},
		{"25.5", ErrInvalidAmount},
		{"25.", ErrInvalidAmount},
		{".50", ErrInvalidAmount},
		{"25.001", ErrScaleExceeded},
		{"025.00", ErrInvalidAmount},
		{"+25.00", ErrInvalidAmount},
		{"25,00", ErrInvalidAmount},
		{" 25.00", ErrInvalidAmount},
		{"1.2.3", ErrInvalidAmount},
		{"92233720368547758.08", ErrOverflow},
		{"99999999999999999999.00", ErrOverflow},
	}
	for _, c := range cases {
		_, err := ParseMoney(c.in, "BRL")
		if !errors.Is(err, c.want) {
			t.Errorf("%q: got %v want %v", c.in, err, c.want)
		}
	}
}

func TestParseMoney_Currency(t *testing.T) {
	for _, cur := range []string{"", "brl", "XXX", "BRLL"} {
		if _, err := ParseMoney("1.00", cur); !errors.Is(err, ErrInvalidCurrency) {
			t.Errorf("%q: got %v", cur, err)
		}
	}
}

func mustMoney(t *testing.T, amount, cur string) Money {
	t.Helper()
	m, err := ParseMoney(amount, cur)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMoney_Arithmetic(t *testing.T) {
	a, b := mustMoney(t, "10.10", "BRL"), mustMoney(t, "0.05", "BRL")
	sum, _ := a.Add(b)
	if sum.Amount() != "10.15" {
		t.Errorf("sum=%s", sum)
	}
	diff, _ := b.Sub(a)
	if diff.Amount() != "-10.05" {
		t.Errorf("diff=%s", diff)
	}
	neg, _ := a.Negate()
	if neg.Amount() != "-10.10" {
		t.Errorf("neg=%s", neg)
	}
	if c, _ := a.Cmp(b); c != 1 {
		t.Errorf("cmp=%d", c)
	}
	// imutabilidade: a e b continuam iguais
	if a.Amount() != "10.10" || b.Amount() != "0.05" {
		t.Error("operands mutated")
	}
}

func TestMoney_CurrencyMismatch(t *testing.T) {
	brl, usd := mustMoney(t, "1.00", "BRL"), mustMoney(t, "1.00", "USD")
	if _, err := brl.Add(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("add: %v", err)
	}
	if _, err := brl.Sub(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("sub: %v", err)
	}
	if _, err := brl.Cmp(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("cmp: %v", err)
	}
}

func TestMoney_Overflow(t *testing.T) {
	max, _ := FromUnits(math.MaxInt64, "BRL")
	min, _ := FromUnits(math.MinInt64, "BRL")
	one := mustMoney(t, "0.01", "BRL")
	if _, err := max.Add(one); !errors.Is(err, ErrOverflow) {
		t.Errorf("add: %v", err)
	}
	if _, err := min.Sub(one); !errors.Is(err, ErrOverflow) {
		t.Errorf("sub: %v", err)
	}
	if _, err := min.Negate(); !errors.Is(err, ErrOverflow) {
		t.Errorf("negate: %v", err)
	}
	if _, err := max.Sub(min); !errors.Is(err, ErrOverflow) {
		t.Errorf("max-min: %v", err)
	}
	if got := min.Amount(); got != "-92233720368547758.08" {
		t.Errorf("min amount=%s", got)
	}
}

func TestMoney_ZeroValueRejected(t *testing.T) {
	var z Money
	ok := mustMoney(t, "1.00", "BRL")
	if _, err := z.Add(ok); !errors.Is(err, ErrUninitialized) {
		t.Errorf("add: %v", err)
	}
	if _, err := z.Negate(); !errors.Is(err, ErrUninitialized) {
		t.Errorf("negate: %v", err)
	}
	if _, err := json.Marshal(z); !errors.Is(err, ErrUninitialized) {
		t.Errorf("marshal: %v", err)
	}
}

func TestMoney_JSON(t *testing.T) {
	m := mustMoney(t, "25.00", "BRL")
	b, err := json.Marshal(m)
	if err != nil || string(b) != `{"amount":"25.00","currency":"BRL"}` {
		t.Fatalf("marshal: %s %v", b, err)
	}
	var back Money
	if err := json.Unmarshal(b, &back); err != nil || back != m {
		t.Fatalf("unmarshal: %v %v", back, err)
	}
	// amount como NÚMERO JSON deve falhar (jamais passa por float)
	if err := json.Unmarshal([]byte(`{"amount":25.00,"currency":"BRL"}`), &back); err == nil {
		t.Error("numeric amount must be rejected")
	}
	if err := json.Unmarshal([]byte(`{"amount":"1e3","currency":"BRL"}`), &back); err == nil {
		t.Error("scientific notation must be rejected")
	}
}

func TestZero(t *testing.T) {
	z, err := Zero("BRL")
	if err != nil || !z.IsZero() || z.Amount() != "0.00" {
		t.Fatalf("%v %v", z, err)
	}
	if _, err := Zero("XXX"); !errors.Is(err, ErrInvalidCurrency) {
		t.Errorf("got %v", err)
	}
}
