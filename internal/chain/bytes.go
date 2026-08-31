package chain

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Bytes holds a byte string that a node may render either as hex or as an array
// of numbers. ed25519 keys and signatures carry serde's own byte encoding, and
// utreexo leaf hashes carry no hex adapter, so both forms appear on the wire.
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
		raw, err := hex.DecodeString(s)
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
