package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"log/slog"

	"goravel/app/facades"
)

// Secret is a string stored encrypted with APP_KEY. The ORM encrypts it on
// write and decrypts it on read, so in Go it holds the plain value:
// string(s) gives it to code that needs it. Printing, logging or JSON
// encoding shows "[redacted]" instead, so a stray %v or an untagged prop
// can't leak it. Model fields of this type still get json:"-".
type Secret string

const redacted = "[redacted]"

// Value encrypts. Each call uses a fresh nonce, so the stored ciphertext
// changes on every save even when the value doesn't.
func (s Secret) Value() (driver.Value, error) {
	ciphertext, err := facades.Crypt().EncryptString(string(s))
	if err != nil {
		return nil, fmt.Errorf("encrypt secret: %w", err)
	}

	return ciphertext, nil
}

// Scan decrypts. It fails if the column wasn't written through Secret, or
// if APP_KEY has changed since.
func (s *Secret) Scan(src any) error {
	var ciphertext string
	switch v := src.(type) {
	case nil:
		*s = ""
		return nil
	case string:
		ciphertext = v
	case []byte:
		ciphertext = string(v)
	default:
		return fmt.Errorf("decrypt secret: unexpected column type %T", src)
	}

	plain, err := facades.Crypt().DecryptString(ciphertext)
	if err != nil {
		return fmt.Errorf("decrypt secret (was APP_KEY changed?): %w", err)
	}
	*s = Secret(plain)

	return nil
}

func (s Secret) String() string   { return redacted }
func (s Secret) GoString() string { return redacted }

func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(redacted) }

func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }
