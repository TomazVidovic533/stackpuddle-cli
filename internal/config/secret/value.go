// Package secret holds a supplied secret value in a type that redacts itself on every print path.
package secret

import (
	"fmt"
	"log/slog"
)

// Redacted is what every print path of a Value shows.
const Redacted = "[redacted]"

// Value is a secret the user supplied. Printing, formatting, encoding or logging it shows Redacted;
// the text leaves only through Reveal. The zero Value holds the empty string and redacts as well.
type Value struct {
	text string
}

// New returns a Value holding s.
func New(s string) Value { return Value{text: s} }

// Reveal returns the held text. Only code that delivers the value to its owner calls it.
func (v Value) Reveal() string { return v.text }

// String returns Redacted.
func (Value) String() string { return Redacted }

// GoString returns Redacted, so %#v does not print the text.
func (Value) GoString() string { return Redacted }

// Format writes Redacted for every verb.
func (Value) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(Redacted)) }

// MarshalJSON encodes Redacted.
func (Value) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }

// MarshalText encodes Redacted.
func (Value) MarshalText() ([]byte, error) { return []byte(Redacted), nil }

// LogValue returns Redacted for structured logs.
func (Value) LogValue() slog.Value { return slog.StringValue(Redacted) }
