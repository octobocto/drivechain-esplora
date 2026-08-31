// Package index turns node blocks into index rows and drives the sync.
package index

import (
	"fmt"

	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/store"
)

// Prepare turns one block into the rows it writes. It holds every rule and it
// touches nothing outside its arguments, so a test needs no node and no
// database.
//
// A block creates coins three ways and spends them two ways:
//
//  1. the body coinbase, which keys on the header merkle root
//  2. a transaction output, which keys on its txid
//  3. a mainchain deposit, which keys on a mainchain outpoint and never
//     appears in the body
//  4. a transaction input, which spends by txid
//  5. a withdrawal bundle, which spends with no transaction at all
func Prepare(
	height uint32,
	hash chain.Hash,
	block *chain.Block,
	blockIndex chain.BlockIndex,
	decoder chain.Decoder,
	blockTime *int64,
) (store.Block, error) {
	if got, want := len(blockIndex.Txs), len(block.Body.Transactions); got != want {
		return store.Block{}, fmt.Errorf(
			"block %s carries %d transactions but its index names %d", hash, want, got)
	}

	out := store.Block{
		Height:     height,
		Hash:       hash,
		PrevHash:   block.Header.PrevSideHash,
		MerkleRoot: block.Header.MerkleRoot,
		MainHash:   block.Header.PrevMainHash,
		BlockTime:  blockTime,
	}

	for i, output := range block.Body.Coinbase {
		row, err := newOutput(
			chain.OutPoint{
				Kind:   chain.KindCoinbase,
				Source: block.Header.MerkleRoot,
				Vout:   uint32(i),
			},
			output, decoder, true)
		if err != nil {
			return store.Block{}, fmt.Errorf("block %s coinbase output %d: %w", hash, i, err)
		}
		out.Creates = append(out.Creates, row)
	}

	for i, tx := range block.Body.Transactions {
		info := blockIndex.Txs[i]
		out.Txs = append(out.Txs, store.Tx{
			Txid:      info.Txid,
			Index:     i,
			SizeBytes: int(info.Size),
			Raw:       info.Raw,
		})

		for vin, input := range tx.Inputs {
			out.Spends = append(out.Spends, store.Spend{
				OutPoint: input.OutPoint,
				Source:   info.Txid,
				Kind:     chain.SpendRegular,
				Vin:      uint32(vin),
			})
		}

		for j, output := range tx.Outputs {
			row, err := newOutput(
				chain.OutPoint{
					Kind:   chain.KindRegular,
					Source: info.Txid,
					Vout:   uint32(j),
				},
				output, decoder, true)
			if err != nil {
				return store.Block{}, fmt.Errorf(
					"block %s transaction %s output %d: %w", hash, info.Txid, j, err)
			}
			out.Creates = append(out.Creates, row)
		}
	}

	for _, deposit := range blockIndex.Deposits {
		if deposit.OutPoint.Kind != chain.KindDeposit {
			return store.Block{}, fmt.Errorf(
				"block %s lists a %s outpoint as a deposit", hash, deposit.OutPoint.Kind)
		}
		row, err := newOutput(deposit.OutPoint, deposit.Output, decoder, true)
		if err != nil {
			return store.Block{}, fmt.Errorf(
				"block %s deposit %s: %w", hash, deposit.OutPoint, err)
		}
		out.Creates = append(out.Creates, row)
	}

	for vin, spend := range blockIndex.BundleSpends {
		out.Spends = append(out.Spends, store.Spend{
			OutPoint: spend.OutPoint,
			Source:   chain.Hash(spend.M6id),
			Kind:     chain.SpendWithdrawal,
			Vin:      uint32(vin),
		})
	}

	return out, nil
}

func newOutput(
	outpoint chain.OutPoint,
	output chain.Output,
	decoder chain.Decoder,
	heightExact bool,
) (store.Output, error) {
	content, err := decoder.DecodeContent(output.Content)
	if err != nil {
		return store.Output{}, err
	}
	return store.Output{
		OutPoint:    outpoint,
		Address:     output.Address,
		ValueSats:   content.ValueSats,
		Content:     output.Content,
		ContentType: content.Type,
		HeightExact: heightExact,
	}, nil
}
