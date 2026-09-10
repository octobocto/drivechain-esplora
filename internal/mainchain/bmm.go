package mainchain

import (
	"bytes"
	"encoding/hex"
)

var m8Tag = []byte{0x00, 0xBF, 0x00}

const (
	blockHashLen = 32
	opReturn     = 0x6a
)

// BmmRequest is a BIP301 M8 BMM request read off the wire.
type BmmRequest struct {
	Slot         uint8
	CriticalHash string
	PrevMainHash string
}

// ParseM8BmmRequestScript reads an M8 request from an output's scriptPubKey:
// OP_RETURN [0x00, 0xBF, 0x00] <slot> <critical hash> <prev mainchain hash>.
// It returns nil for any other script, so it can run over a whole mempool.
func ParseM8BmmRequestScript(script []byte) *BmmRequest {
	const messageLen = 3 + 1 + 2*blockHashLen
	if len(script) != messageLen+2 {
		return nil
	}
	if script[0] != opReturn || int(script[1]) != messageLen {
		return nil
	}
	if !bytes.Equal(script[2:5], m8Tag) {
		return nil
	}
	prevMain := make([]byte, blockHashLen)
	copy(prevMain, script[6+blockHashLen:])
	// bitcoind prints mainchain block hashes in reverse byte order.
	reverseBytes(prevMain)
	return &BmmRequest{
		Slot:         script[5],
		CriticalHash: hex.EncodeToString(script[6 : 6+blockHashLen]),
		PrevMainHash: hex.EncodeToString(prevMain),
	}
}

func reverseBytes(b []byte) {
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
}
