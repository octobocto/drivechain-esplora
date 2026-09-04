package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/octobocto/drivechain-esplora/internal/api"
	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/store"
)

func withdrawal(sats, mainFee uint64, mainAddress string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(
		`{"Withdrawal":{"value":%d,"main_fee":%d,"main_address":%q}}`,
		sats, mainFee, mainAddress))
}

// An explorer reads one chain-wide feed. A deposit never appears in a block
// body, and a transaction that pays out reads as a withdrawal, so the feed
// must name all three kinds.
func TestExplorerFeedNamesEveryKind(t *testing.T) {
	h := newHarness(t)

	alice, bob := addr(1), addr(2)
	genesisHash, secondHash := hash(0xa0), hash(0xa1)
	merkle0, merkle1 := hash(0x80), hash(0x81)
	transferTxid, withdrawTxid := hash(0x11), hash(0x12)
	depositTxid := hash(0xdd)

	h.node.AddBlock(genesisHash, &chain.Block{
		Header: chain.Header{MerkleRoot: merkle0, PrevMainHash: chain.BitcoinHash(hash(0x40))},
		Body:   chain.Body{Coinbase: []chain.Output{{Address: alice, Content: value(100000)}}},
	}, chain.BlockIndex{})

	prev := genesisHash
	h.node.AddBlock(secondHash, &chain.Block{
		Header: chain.Header{
			MerkleRoot: merkle1, PrevSideHash: &prev,
			PrevMainHash: chain.BitcoinHash(hash(0x41)),
		},
		Body: chain.Body{
			Transactions: []chain.Transaction{
				{
					Inputs: []chain.Input{{OutPoint: chain.OutPoint{
						Kind: chain.KindCoinbase, Source: merkle0, Vout: 0,
					}}},
					Outputs: []chain.Output{{Address: bob, Content: value(90000)}},
				},
				{
					Inputs: []chain.Input{{OutPoint: chain.OutPoint{
						Kind: chain.KindRegular, Source: transferTxid, Vout: 0,
					}}},
					Outputs: []chain.Output{{
						Address: bob,
						Content: withdrawal(50000, 1200, "bc1qexample"),
					}},
				},
			},
		},
	}, chain.BlockIndex{
		Txs: []chain.TxInfo{
			{Txid: transferTxid, Size: 200, Raw: chain.Bytes{1}},
			{Txid: withdrawTxid, Size: 240, Raw: chain.Bytes{2}},
		},
		Deposits: []chain.Deposit{{
			OutPoint: chain.OutPoint{Kind: chain.KindDeposit, Source: depositTxid, Vout: 0},
			Output:   chain.Output{Address: alice, Content: value(123456)},
		}},
	})

	h.sync(t)

	var feed []api.Activity
	if status := h.get(t, "/txs/recent", &feed); status != 200 {
		t.Fatalf("recent activity status = %d", status)
	}

	kinds := map[string]string{}
	for _, row := range feed {
		kinds[row.ID] = row.Kind
	}
	for id, want := range map[string]string{
		transferTxid.String(): store.KindTransfer,
		withdrawTxid.String(): store.KindWithdrawal,
	} {
		if got := kinds[id]; got != want {
			t.Errorf("%s reads as %q, want %q", id, got, want)
		}
	}
	if len(feed) != 3 {
		t.Fatalf("the feed holds %d rows, want 3", len(feed))
	}

	var deposits int
	for _, row := range feed {
		if row.Kind == store.KindDeposit {
			deposits++
			if row.Value != 123456 {
				t.Errorf("the deposit is worth %d, want 123456", row.Value)
			}
		}
	}
	if deposits != 1 {
		t.Errorf("the feed holds %d deposits, want 1", deposits)
	}

	// One block's own feed carries the same three rows.
	var inBlock []api.Activity
	if status := h.get(t, "/block/"+secondHash.String()+"/activity", &inBlock); status != 200 {
		t.Fatalf("block activity status = %d", status)
	}
	if len(inBlock) != 3 {
		t.Errorf("block one carries %d rows, want 3", len(inBlock))
	}
}

// A block card names what the block cost and which mainchain block carried its
// bid. The index has no enforcer here, so the height stays empty.
func TestBlockCarriesFeesAndItsMainchainLink(t *testing.T) {
	h := newHarness(t)

	alice := addr(1)
	genesisHash, secondHash := hash(0xa0), hash(0xa1)
	merkle0, merkle1 := hash(0x80), hash(0x81)
	txid := hash(0x11)
	mainHash := chain.BitcoinHash(hash(0x41))

	h.node.AddBlock(genesisHash, &chain.Block{
		Header: chain.Header{MerkleRoot: merkle0, PrevMainHash: chain.BitcoinHash(hash(0x40))},
		Body:   chain.Body{Coinbase: []chain.Output{{Address: alice, Content: value(100000)}}},
	}, chain.BlockIndex{})

	prev := genesisHash
	h.node.AddBlock(secondHash, &chain.Block{
		Header: chain.Header{
			MerkleRoot: merkle1, PrevSideHash: &prev, PrevMainHash: mainHash,
		},
		Body: chain.Body{Transactions: []chain.Transaction{{
			Inputs: []chain.Input{{OutPoint: chain.OutPoint{
				Kind: chain.KindCoinbase, Source: merkle0, Vout: 0,
			}}},
			Outputs: []chain.Output{{Address: alice, Content: value(99000)}},
		}}},
	}, chain.BlockIndex{
		Txs: []chain.TxInfo{{Txid: txid, Size: 200, Raw: chain.Bytes{1}}},
	})

	h.sync(t)

	var block api.Block
	if status := h.get(t, "/block/"+secondHash.String(), &block); status != 200 {
		t.Fatalf("block status = %d", status)
	}
	if block.Fees != 1000 {
		t.Errorf("block fees = %d, want 1000", block.Fees)
	}
	if block.Value != 99000 {
		t.Errorf("block value = %d, want 99000", block.Value)
	}
	if block.MainchainHash != mainHash.String() {
		t.Errorf("mainchain hash = %s, want %s", block.MainchainHash, mainHash)
	}
	if block.MainchainHeight != nil {
		t.Errorf("mainchain height = %d, want none without an enforcer", *block.MainchainHeight)
	}

	var list []api.Block
	if status := h.get(t, "/blocks", &list); status != 200 {
		t.Fatalf("blocks status = %d", status)
	}
	if len(list) != 2 || list[0].Fees != 1000 {
		t.Fatalf("blocks = %+v, want the tip to carry 1000 sats of fees", list)
	}
}

// A light client runs no node, so the index reads the withdrawal bundle for it.
func TestWithdrawalBundleReachesALightClient(t *testing.T) {
	h := newHarness(t)

	height := uint32(812443)
	h.node.SetWithdrawalBundle(json.RawMessage(`{"height_created":812401}`), &height)

	var state api.WithdrawalState
	if status := h.get(t, "/drivechain/withdrawals", &state); status != 200 {
		t.Fatalf("withdrawals status = %d", status)
	}
	if string(state.Bundle) != `{"height_created":812401}` {
		t.Errorf("bundle = %s, want the node's own json", state.Bundle)
	}
	if state.LastFailedHeight == nil || *state.LastFailedHeight != height {
		t.Errorf("last failed height = %v, want %d", state.LastFailedHeight, height)
	}
}

// fakeHeights answers the mainchain height of a block, the way an enforcer
// does.
type fakeHeights struct {
	byHash map[string]uint32
	calls  int
}

func (f *fakeHeights) BlockHeight(_ context.Context, hash string) (uint32, bool, error) {
	f.calls++
	height, ok := f.byHash[hash]
	return height, ok, nil
}

// A chain indexed before the mainchain height existed fills it in later, and a
// chain with nothing left to fill stops asking.
func TestBackfillFillsTheMainchainHeight(t *testing.T) {
	h := newHarness(t)

	alice := addr(1)
	genesisHash := hash(0xa0)
	mainHash := chain.BitcoinHash(hash(0x40))

	h.node.AddBlock(genesisHash, &chain.Block{
		Header: chain.Header{MerkleRoot: hash(0x80), PrevMainHash: mainHash},
		Body:   chain.Body{Coinbase: []chain.Output{{Address: alice, Content: value(50000)}}},
	}, chain.BlockIndex{})

	h.sync(t)

	var before api.Block
	h.get(t, "/block/"+genesisHash.String(), &before)
	if before.MainchainHeight != nil {
		t.Fatalf("mainchain height = %d, want none before an enforcer answers", *before.MainchainHeight)
	}

	heights := &fakeHeights{byHash: map[string]uint32{mainHash.String(): 996799}}
	h.syncer.Heights = heights
	h.sync(t)

	var after api.Block
	h.get(t, "/block/"+genesisHash.String(), &after)
	if after.MainchainHeight == nil || *after.MainchainHeight != 996799 {
		t.Fatalf("mainchain height = %v, want 996799", after.MainchainHeight)
	}

	// Nothing is left to fill, so the next pass asks the enforcer nothing.
	filled := heights.calls
	h.sync(t)
	if heights.calls != filled {
		t.Errorf("the enforcer answered %d more times with nothing left to fill",
			heights.calls-filled)
	}
}
