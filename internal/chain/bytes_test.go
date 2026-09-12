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

// A bitnames key is bech32, which no hex decoder reads. The node's own text is
// the value, and a hex decode of it would either fail or corrupt it.
func TestByteStringKeepsTextThatIsNotHex(t *testing.T) {
	const key = "bn-svk179g6xez5ud4xvhjlgugc4zu8tlt0ap2lm7qh7gh5mzr43zqua9kq4d90yn"

	var b ByteString
	if err := json.Unmarshal([]byte(`"`+key+`"`), &b); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b.Text != key {
		t.Errorf("text = %q, want %q", b.Text, key)
	}
	if b.Bytes != nil {
		t.Errorf("bytes = %s, want none for text that is not hex", b.Bytes)
	}
	if b.String() != key {
		t.Errorf("string = %q, want %q", b.String(), key)
	}
}

// An encode gives back the rendering the node sent. An array of numbers is the
// one form that reads back as hex, which names the same bytes.
func TestByteStringRoundTripsEveryShape(t *testing.T) {
	cases := map[string]struct {
		wire string
		want string
	}{
		"bech32 text":      {wire: `"bn-svk1lqw3s30czurfhg7kdcm5nwnp9xqps8fa0tqswfmx0ysle0sfcs3sx6lhjw"`, want: `"bn-svk1lqw3s30czurfhg7kdcm5nwnp9xqps8fa0tqswfmx0ysle0sfcs3sx6lhjw"`},
		"prefixed hex":     {wire: `"0x0102ff"`, want: `"0x0102ff"`},
		"plain hex":        {wire: `"0102ff"`, want: `"0102ff"`},
		"array of numbers": {wire: `[1,2,255]`, want: `"0102ff"`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var b ByteString
			if err := json.Unmarshal([]byte(tc.wire), &b); err != nil {
				t.Fatalf("decode: %v", err)
			}
			raw, err := json.Marshal(b)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if string(raw) != tc.want {
				t.Errorf("round trip = %s, want %s", raw, tc.want)
			}
		})
	}
}

func TestByteStringReadsTheBytesOfHex(t *testing.T) {
	for _, wire := range []string{`"0x0102ff"`, `"0102ff"`, `[1,2,255]`} {
		var b ByteString
		if err := json.Unmarshal([]byte(wire), &b); err != nil {
			t.Fatalf("decode %s: %v", wire, err)
		}
		if b.Bytes.String() != "0102ff" {
			t.Errorf("%s gave bytes %s, want 0102ff", wire, b.Bytes)
		}
	}
}
