package chain

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Bytes holds a byte string that a node may render as hex, as hex with a "0x"
// prefix, or as an array of numbers. ed25519 keys and signatures carry serde's
// own byte encoding, and utreexo leaf hashes carry no hex adapter, so every
// form appears on the wire.
type Bytes []byte

func (b Bytes) String() string { return hex.EncodeToString(b) }

func (b Bytes) MarshalJSON() ([]byte, error) { return json.Marshal(b.String()) }

func (b *Bytes) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("byte string is empty")
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("decode byte string: %w", err)
		}
		raw, err := decodeHex(s)
		if err != nil {
			return fmt.Errorf("decode byte string %q: %w", s, err)
		}
		*b = raw
		return nil
	}
	var numbers []byte
	if err := json.Unmarshal(data, &numbers); err != nil {
		return fmt.Errorf("decode byte array: %w", err)
	}
	*b = numbers
	return nil
}

// decodeHex reads hex with or without a "0x" prefix.
func decodeHex(s string) ([]byte, error) {
	return hex.DecodeString(strings.TrimPrefix(s, "0x"))
}

// Variant reads the one key an externally tagged serde enum writes. A variant
// with no field arrives as a bare string; every other variant arrives as a
// one-key object. The payload is nil for the bare form.
func Variant(raw json.RawMessage) (string, json.RawMessage, error) {
	var tag string
	if err := json.Unmarshal(raw, &tag); err == nil {
		return tag, nil, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", nil, fmt.Errorf("read output content %s: %w", raw, err)
	}
	if len(obj) != 1 {
		return "", nil, fmt.Errorf("output content %s names %d variants, want 1", raw, len(obj))
	}
	for tag, payload := range obj {
		return tag, payload, nil
	}
	return "", nil, fmt.Errorf("output content %s names no variant", raw)
}
