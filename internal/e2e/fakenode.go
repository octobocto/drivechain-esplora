// Package e2e drives the whole pipeline: a node, the syncer, Postgres, and the
// HTTP API.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/octobocto/drivechain-esplora/internal/chain"
)

// FakeNode serves the node RPC surface this service reads, over a real HTTP
// listener, so the client and its decoders run exactly as in production.
type FakeNode struct {
	mu     sync.Mutex
	chainT []chain.Hash
	blocks map[chain.Hash]*chain.Block
	index  map[chain.Hash]chain.BlockIndex
	// submitted holds every transaction a broadcast handed over, so a test
	// reads what reached the node.
	submitted []json.RawMessage
	// mempool holds the transactions the node would mine next.
	mempool []chain.Transaction
	// bundle is what pending_withdrawal_bundle answers.
	bundle json.RawMessage
	// failedHeight is what latest_failed_withdrawal_bundle_height answers.
	failedHeight *uint32

	server *httptest.Server
}

// NewFakeNode starts a node with an empty chain.
func NewFakeNode() *FakeNode {
	n := &FakeNode{
		blocks: make(map[chain.Hash]*chain.Block),
		index:  make(map[chain.Hash]chain.BlockIndex),
	}
	n.server = httptest.NewServer(http.HandlerFunc(n.serve))
	return n
}

// URL is the node address.
func (n *FakeNode) URL() string { return n.server.URL }

// Submitted lists the transactions a broadcast handed over, in order.
func (n *FakeNode) Submitted() []json.RawMessage {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]json.RawMessage(nil), n.submitted...)
}

// SetWithdrawalBundle sets what the bundle routes answer.
func (n *FakeNode) SetWithdrawalBundle(bundle json.RawMessage, failedHeight *uint32) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.bundle = bundle
	n.failedHeight = failedHeight
}

// Close stops the listener.
func (n *FakeNode) Close() { n.server.Close() }

// AddBlock appends one block to the active chain.
func (n *FakeNode) AddBlock(hash chain.Hash, block *chain.Block, index chain.BlockIndex) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.chainT = append(n.chainT, hash)
	n.blocks[hash] = block
	n.index[hash] = index
}

// SetMempool names the transactions the node would mine next. The block
// template carries them, and that template is the whole mempool view.
func (n *FakeNode) SetMempool(txs []chain.Transaction) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.mempool = txs
}

// Reorg drops every block above height, so the next sync pass finds a fork.
func (n *FakeNode) Reorg(height uint32) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if int(height)+1 < len(n.chainT) {
		n.chainT = n.chainT[:height+1]
	}
}

type request struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (n *FakeNode) serve(w http.ResponseWriter, r *http.Request) {
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, rpcErr := n.dispatch(req)
	body := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if rpcErr != nil {
		body["error"] = map[string]any{"code": -32000, "message": rpcErr.Error()}
	} else {
		body["result"] = result
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (n *FakeNode) dispatch(req request) (any, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	switch req.Method {
	case "getblockcount":
		return len(n.chainT), nil

	case "get_best_sidechain_block_hash":
		if len(n.chainT) == 0 {
			return nil, nil
		}
		return n.chainT[len(n.chainT)-1], nil

	case "get_block_hash":
		var params []uint32
		if err := json.Unmarshal(req.Params, &params); err != nil || len(params) != 1 {
			return nil, fmt.Errorf("get_block_hash takes one height")
		}
		if int(params[0]) >= len(n.chainT) {
			return nil, nil
		}
		return n.chainT[params[0]], nil

	case "get_block":
		hash, err := hashParam(req.Params)
		if err != nil {
			return nil, err
		}
		block, ok := n.blocks[hash]
		if !ok {
			return nil, nil
		}
		return block, nil

	case "get_block_index":
		hash, err := hashParam(req.Params)
		if err != nil {
			return nil, err
		}
		index, ok := n.index[hash]
		if !ok {
			return nil, fmt.Errorf("no index for block %s", hash)
		}
		return index, nil

	case "pending_withdrawal_bundle":
		return n.bundle, nil

	case "latest_failed_withdrawal_bundle_height":
		return n.failedHeight, nil

	case "get_block_template":
		var merkle chain.Hash
		if len(n.chainT) > 0 {
			merkle = n.blocks[n.chainT[len(n.chainT)-1]].Header.MerkleRoot
		}
		return chain.BlockTemplate{
			Block: chain.Block{
				Header: chain.Header{MerkleRoot: merkle},
				Body:   chain.Body{Transactions: n.mempool},
			},
		}, nil

	case "submit_transaction":
		var params []json.RawMessage
		if err := json.Unmarshal(req.Params, &params); err != nil || len(params) != 1 {
			return nil, fmt.Errorf("submit_transaction takes one transaction")
		}
		n.submitted = append(n.submitted, params[0])
		return chain.Hash{1, 2, 3}.String(), nil

	default:
		return nil, fmt.Errorf("method %q is not served", req.Method)
	}
}

func hashParam(raw json.RawMessage) (chain.Hash, error) {
	var params []chain.Hash
	if err := json.Unmarshal(raw, &params); err != nil || len(params) != 1 {
		return chain.Hash{}, fmt.Errorf("the method takes one block hash")
	}
	return params[0], nil
}
