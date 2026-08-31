package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/mr-tron/base58"
)

// Hash is a 32-byte blake3 digest. Sidechain hashes render as plain lowercase
// hex. Bitcoin hashes render reversed; use BitcoinHash for those.
type Hash [32]byte

func (h Hash) String() string { return hex.EncodeToString(h[:]) }

func (h Hash) MarshalJSON() ([]byte, error) { return json.Marshal(h.String()) }

func (h *Hash) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("hash is not a string: %w", err)
	}
	return h.parse(s)
}

func (h *Hash) parse(s string) error {
	raw, err := hex.DecodeString(s)
	if err != nil {
		return fmt.Errorf("decode hash %q: %w", s, err)
	}
	if len(raw) != 32 {
		return fmt.Errorf("hash %q is %d bytes, want 32", s, len(raw))
	}
	copy(h[:], raw)
	return nil
}

// ParseHash reads a 64-character lowercase hex digest.
func ParseHash(s string) (Hash, error) {
	var h Hash
	err := h.parse(s)
	return h, err
}

// BitcoinHash is a 32-byte mainchain hash. rust-bitcoin renders it in reverse
// byte order, so the wire form is the reverse of the stored bytes.
type BitcoinHash [32]byte

func (h BitcoinHash) String() string {
	var flipped [32]byte
	for i, b := range h {
		flipped[31-i] = b
	}
	return hex.EncodeToString(flipped[:])
}

func (h BitcoinHash) MarshalJSON() ([]byte, error) { return json.Marshal(h.String()) }

func (h *BitcoinHash) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("bitcoin hash is not a string: %w", err)
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return fmt.Errorf("decode bitcoin hash %q: %w", s, err)
	}
	if len(raw) != 32 {
		return fmt.Errorf("bitcoin hash %q is %d bytes, want 32", s, len(raw))
	}
	for i, b := range raw {
		h[31-i] = b
	}
	return nil
}

// Address is a 20-byte sidechain address. The wire form is base58 with no
// version byte and no checksum.
type Address [20]byte

func (a Address) String() string { return base58.Encode(a[:]) }

func (a Address) MarshalJSON() ([]byte, error) { return json.Marshal(a.String()) }

func (a *Address) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("address is not a string: %w", err)
	}
	parsed, err := ParseAddress(s)
	if err != nil {
		return err
	}
	*a = parsed
	return nil
}

// ParseAddress reads the base58 form of a 20-byte sidechain address.
func ParseAddress(s string) (Address, error) {
	var a Address
	raw, err := base58.Decode(s)
	if err != nil {
		return a, fmt.Errorf("decode address %q: %w", s, err)
	}
	if len(raw) != 20 {
		return a, fmt.Errorf("address %q is %d bytes, want 20", s, len(raw))
	}
	copy(a[:], raw)
	return a, nil
}

// ScriptHash is the sha256 of the address bytes. These chains have no script,
// so this is what an Electrum-style client keys on instead.
func (a Address) ScriptHash() [32]byte { return sha256.Sum256(a[:]) }
