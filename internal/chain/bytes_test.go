package chain

import (
	"encoding/json"
	"testing"
)

// A bitnames node writes a signature as hex with a "0x" prefix, and a thunder
// node writes the same bytes with no prefix.
func TestBytesReadsHexWithAPrefix(t *testing.T) {
	cases := map[string]string{
		"prefixed": `"0x0102ff"`,
		"plain":    `"0102ff"`,
	}

	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			var b Bytes
			if err := json.Unmarshal([]byte(wire), &b); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if b.String() != "0102ff" {
				t.Errorf("bytes = %s, want 0102ff", b)
			}
		})
	}
}

func TestBytesRejectsTextThatIsNotHex(t *testing.T) {
	var b Bytes
	if err := json.Unmarshal([]byte(`"0xzz"`), &b); err == nil {
		t.Fatal("want an error for text that is not hex, got none")
	}
}
