package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// Currency é um código ISO 4217. Lista permitida pequena de propósito:
// acrescente moedas aqui conforme necessário.
type Currency string

var allowedCurrencies = map[Currency]bool{"BRL": true, "USD": true, "EUR": true}

func ParseCurrency(s string) (Currency, error) {
	c := Currency(s)
	if !allowedCurrencies[c] {
		return "", fmt.Errorf("%w: %q", ErrInvalidCurrency, s)
	}
	return c, nil
}

// Money é um value object IMUTÁVEL: campos privados e nenhum método altera o receptor.
// Representação: int64 em centavos (escala fixa 2). Faixa: ±92.233.720.368.547.758,07.
// O valor zero de Money (moeda vazia) é inválido e rejeitado por toda operação.
type Money struct {
	units    int64
	currency Currency
}

// ParseMoney é o parser das ENTRADAS EXTERNAS. Formato aceito: ^(0|[1-9][0-9]*)\.[0-9]{2}$
// Rejeita: vazio, sinal, NaN, Infinity, notação científica, "25", "25.5", "25.001", "025.00".
// Só aceitamos a forma canônica; logo a normalização antes do hash é a identidade.
func ParseMoney(amount, currency string) (Money, error) {
	cur, err := ParseCurrency(currency)
	if err != nil {
		return Money{}, err
	}
	units, err := parseUnits(amount)
	if err != nil {
		return Money{}, err
	}
	return Money{units: units, currency: cur}, nil
}

func parseUnits(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("%w: empty", ErrInvalidAmount)
	}
	if s[0] == '-' {
		return 0, ErrNegativeAmount
	}
	dot := -1
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '.' && dot == -1:
			dot = i
		case s[i] >= '0' && s[i] <= '9':
		default: // pega 'e', 'N', 'I', '+', ',', espaços, segundo '.', etc.
			return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
		}
	}
	if dot <= 0 { // sem ponto, ou ponto no início
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
	}
	whole, frac := s[:dot], s[dot+1:]
	if len(frac) > 2 {
		return 0, fmt.Errorf("%w: %q", ErrScaleExceeded, s)
	}
	if len(frac) != 2 || (len(whole) > 1 && whole[0] == '0') {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, s)
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil { // só pode ser fora da faixa (já validamos os dígitos)
		return 0, fmt.Errorf("%w: %q", ErrOverflow, s)
	}
	cents := int64(frac[0]-'0')*10 + int64(frac[1]-'0')
	if w > (math.MaxInt64-cents)/100 {
		return 0, fmt.Errorf("%w: %q", ErrOverflow, s)
	}
	return w*100 + cents, nil
}

// FromUnits cria Money a partir de centavos (uso interno: cálculos e reidratação).
// Aceita negativos porque diferenças internas podem ser negativas.
func FromUnits(units int64, currency Currency) (Money, error) {
	if !allowedCurrencies[currency] {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
	}
	return Money{units: units, currency: currency}, nil
}

// Zero devolve 0.00 na moeda informada.
func Zero(currency Currency) (Money, error) { return FromUnits(0, currency) }

func (m Money) Currency() Currency { return m.currency }
func (m Money) Units() int64       { return m.units }
func (m Money) IsValid() bool      { return allowedCurrencies[m.currency] }
func (m Money) IsZero() bool       { return m.units == 0 }
func (m Money) IsPositive() bool   { return m.units > 0 }
func (m Money) IsNegative() bool   { return m.units < 0 }

func (m Money) compatible(o Money) error {
	if !m.IsValid() || !o.IsValid() {
		return ErrUninitialized
	}
	if m.currency != o.currency {
		return fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, m.currency, o.currency)
	}
	return nil
}

func (m Money) Add(o Money) (Money, error) {
	if err := m.compatible(o); err != nil {
		return Money{}, err
	}
	if (o.units > 0 && m.units > math.MaxInt64-o.units) || (o.units < 0 && m.units < math.MinInt64-o.units) {
		return Money{}, ErrOverflow
	}
	return Money{m.units + o.units, m.currency}, nil
}

func (m Money) Sub(o Money) (Money, error) {
	if err := m.compatible(o); err != nil {
		return Money{}, err
	}
	if (o.units > 0 && m.units < math.MinInt64+o.units) || (o.units < 0 && m.units > math.MaxInt64+o.units) {
		return Money{}, ErrOverflow
	}
	return Money{m.units - o.units, m.currency}, nil
}

func (m Money) Negate() (Money, error) {
	if !m.IsValid() {
		return Money{}, ErrUninitialized
	}
	if m.units == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return Money{-m.units, m.currency}, nil
}

// Cmp devolve -1, 0 ou 1. Moedas diferentes é erro, não "false".
func (m Money) Cmp(o Money) (int, error) {
	if err := m.compatible(o); err != nil {
		return 0, err
	}
	switch {
	case m.units < o.units:
		return -1, nil
	case m.units > o.units:
		return 1, nil
	}
	return 0, nil
}

// Amount devolve a string decimal com 2 casas (ex.: "25.00", "-0.05").
func (m Money) Amount() string {
	neg := m.units < 0
	var abs uint64 // uint64 evita overflow ao negar MinInt64
	if neg {
		abs = uint64(-(m.units + 1)) + 1
	} else {
		abs = uint64(m.units)
	}
	s := fmt.Sprintf("%d.%02d", abs/100, abs%100)
	if neg {
		s = "-" + s
	}
	return s
}

func (m Money) String() string { return m.Amount() + " " + string(m.currency) }

type moneyJSON struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func (m Money) MarshalJSON() ([]byte, error) {
	if !m.IsValid() {
		return nil, ErrUninitialized
	}
	return json.Marshal(moneyJSON{m.Amount(), string(m.currency)})
}

// UnmarshalJSON usa o parser ESTRITO. Se "amount" vier como número JSON (25.00),
// o decoder falha ao ler em string: nunca passa por float.
func (m *Money) UnmarshalJSON(b []byte) error {
	var j moneyJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAmount, err)
	}
	v, err := ParseMoney(j.Amount, j.Currency)
	if err != nil {
		return err
	}
	*m = v
	return nil
}
