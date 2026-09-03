package thunder

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"lukechampine.com/blake3"

	"github.com/octobocto/drivechain-esplora/internal/chain"
)

// leafHashSize is the width of a utreexo leaf hash.
const leafHashSize = 32

// writer builds the borsh encoding a txid hashes over. Every rule here mirrors
// the rust types crate, so a change on either side changes every txid.
type writer struct {
	buf bytes.Buffer
}

func (w *writer) u8(v uint8) { w.buf.WriteByte(v) }

// u32 writes a little-endian u32, which is also how borsh writes the length of
// a sequence.
func (w *writer) u32(v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	w.buf.Write(b[:])
}

func (w *writer) u64(v uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	w.buf.Write(b[:])
}

// fixed writes an array, which carries no length.
func (w *writer) fixed(b []byte) { w.buf.Write(b) }

// slice writes a byte slice, which carries a u32 length first.
func (w *writer) slice(b []byte) {
	w.u32(uint32(len(b)))
	w.buf.Write(b)
}

// encodeTx writes the canonical encoding of one transaction. The node skips the
// utreexo proof, so this holds the inputs and the outputs alone.
func encodeTx(tx chain.Transaction) ([]byte, error) {
	var w writer

	w.u32(uint32(len(tx.Inputs)))
	for i, in := range tx.Inputs {
		if len(in.LeafHash) != leafHashSize {
			return nil, fmt.Errorf(
				"input %d carries a %d byte leaf hash, want %d",
				i, len(in.LeafHash), leafHashSize)
		}
		key := in.OutPoint.Key()
		w.fixed(key[:])
		w.fixed(in.LeafHash)
	}

	w.u32(uint32(len(tx.Outputs)))
	for i, out := range tx.Outputs {
		w.fixed(out.Address[:])
		if err := encodeContent(&w, out.Content); err != nil {
			return nil, fmt.Errorf("output %d: %w", i, err)
		}
	}
	return w.buf.Bytes(), nil
}

// encodeContent writes one output payload. An amount is a u64 of sats, and a
// mainchain address writes as the script pubkey it pays.
func encodeContent(w *writer, raw json.RawMessage) error {
	var c content
	if err := json.Unmarshal(raw, &c); err != nil {
		return fmt.Errorf("decode thunder output content: %w", err)
	}
	switch {
	case c.Value != nil:
		w.u8(0)
		w.u64(*c.Value)
		return nil
	case c.Withdrawal != nil:
		script, err := mainScriptPubKey(c.Withdrawal.MainAddress)
		if err != nil {
			return err
		}
		w.u8(1)
		w.u64(max(c.Withdrawal.Value, c.Withdrawal.ValueSats))
		w.u64(max(c.Withdrawal.MainFee, c.Withdrawal.MainFeeSats))
		w.slice(script)
		return nil
	default:
		return fmt.Errorf("thunder output content %s names no known variant", raw)
	}
}

// mainNetworks are the parameter sets a mainchain address can carry. The script
// a withdrawal hashes over comes from the address itself, so the first set that
// reads the address gives the right script.
var mainNetworks = []*chaincfg.Params{
	&chaincfg.MainNetParams,
	&chaincfg.TestNet3Params,
	&chaincfg.RegressionNetParams,
}

// mainScriptPubKey turns the mainchain address of a withdrawal into the script
// it pays.
func mainScriptPubKey(address string) ([]byte, error) {
	for _, params := range mainNetworks {
		decoded, err := btcutil.DecodeAddress(address, params)
		if err != nil || !decoded.IsForNet(params) {
			continue
		}
		script, err := txscript.PayToAddrScript(decoded)
		if err != nil {
			return nil, fmt.Errorf("build the script of %q: %w", address, err)
		}
		return script, nil
	}
	return nil, fmt.Errorf("withdrawal address %q belongs to no known network", address)
}

// IdentifyTx names one transaction: its txid, its canonical size, and the
// encoding both come from.
//
// Size mirrors the node's own canonical_size, which divides the encoding by
// eight, so a transaction reports one size before and after a block carries it.
func (Decoder) IdentifyTx(tx chain.Transaction) (chain.TxInfo, error) {
	raw, err := encodeTx(tx)
	if err != nil {
		return chain.TxInfo{}, err
	}
	return chain.TxInfo{
		Txid: blake3.Sum256(raw),
		Size: uint64(len(raw) / 8),
		Raw:  raw,
	}, nil
}
