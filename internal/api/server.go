package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/mainchain"
	"github.com/octobocto/drivechain-esplora/internal/service"
	"github.com/octobocto/drivechain-esplora/internal/store"
)

// PageSize is how many confirmed rows one history page carries. It matches what
// an Esplora client expects, and a client pages until it sees a shorter one.
const PageSize = 25

// blockListSize is how many headers /blocks returns.
const blockListSize = 10

// recentListSize is how many rows /mempool/recent returns.
const recentListSize = 10

// activityListSize is how many rows /txs/recent returns.
const activityListSize = 25

// FeeRateSatPerVByte is what /fee-estimates answers for every target. These
// chains have no fee market, and a client calls this before every send.
const FeeRateSatPerVByte = 1.0

// Broadcaster hands a signed transaction to the node.
type Broadcaster interface {
	Broadcast(ctx context.Context, tx json.RawMessage) (chain.Hash, error)
}

// Withdrawals reads the node's withdrawal bundle state. A deployment with no
// node leaves this nil, and the withdrawal route then says it has no source.
type Withdrawals interface {
	Pending(ctx context.Context) (json.RawMessage, error)
	LastFailedHeight(ctx context.Context) (*uint32, error)
}

// Mainchain reads the BIP300 view of the mainchain. A deployment with no
// enforcer behind it leaves this nil, and the drivechain routes then answer
// that they have no source.
type Mainchain interface {
	Sidechains(ctx context.Context) ([]mainchain.Sidechain, error)
	Ctip(ctx context.Context, slot uint32) (mainchain.Ctip, bool, error)
}

// Server answers Esplora requests from the index.
type Server struct {
	stores      *service.Service[*store.Store]
	broadcaster Broadcaster
	mainchain   Mainchain
	withdrawals Withdrawals
	log         *slog.Logger
}

// NewServer builds the API. The store sits behind a service wrapper, so the
// listener starts even when the database is down.
func NewServer(
	stores *service.Service[*store.Store],
	broadcaster Broadcaster,
	mainchain Mainchain,
	withdrawals Withdrawals,
	log *slog.Logger,
) *Server {
	return &Server{
		stores:      stores,
		broadcaster: broadcaster,
		mainchain:   mainchain,
		withdrawals: withdrawals,
		log:         log,
	}
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /blocks/tip/height", s.tipHeight)
	mux.HandleFunc("GET /blocks/tip/hash", s.tipHash)
	mux.HandleFunc("GET /blocks", s.blocks)
	mux.HandleFunc("GET /blocks/{start_height}", s.blocks)
	mux.HandleFunc("GET /block-height/{height}", s.blockHeight)
	mux.HandleFunc("GET /block/{hash}", s.block)
	mux.HandleFunc("GET /block/{hash}/header", s.blockHeader)
	mux.HandleFunc("GET /block/{hash}/status", s.blockStatus)
	mux.HandleFunc("GET /block/{hash}/txids", s.blockTxids)
	mux.HandleFunc("GET /block/{hash}/txid/{index}", s.blockTxid)
	mux.HandleFunc("GET /block/{hash}/txs", s.blockTxs)
	mux.HandleFunc("GET /block/{hash}/txs/{start_index}", s.blockTxs)

	mux.HandleFunc("GET /tx/{txid}", s.tx)
	mux.HandleFunc("GET /tx/{txid}/status", s.txStatus)
	mux.HandleFunc("GET /tx/{txid}/hex", s.txHex)
	mux.HandleFunc("GET /tx/{txid}/raw", s.txHex)
	mux.HandleFunc("GET /tx/{txid}/outspends", s.outspends)
	mux.HandleFunc("GET /tx/{txid}/outspend/{vout}", s.outspend)
	mux.HandleFunc("POST /tx", s.broadcast)

	for _, prefix := range []string{"address", "scripthash"} {
		mux.HandleFunc("GET /"+prefix+"/{key}", s.addressInfo)
		mux.HandleFunc("GET /"+prefix+"/{key}/utxo", s.addressUTXOs)
		mux.HandleFunc("GET /"+prefix+"/{key}/txs", s.addressTxs)
		mux.HandleFunc("GET /"+prefix+"/{key}/txs/chain", s.addressTxs)
		mux.HandleFunc("GET /"+prefix+"/{key}/txs/chain/{last_seen}", s.addressTxs)
		mux.HandleFunc("GET /"+prefix+"/{key}/txs/mempool", s.addressMempoolTxs)
	}
	mux.HandleFunc("GET /address/{key}/deposits", s.addressDeposits)

	mux.HandleFunc("GET /deposit/{txid}", s.deposit)
	mux.HandleFunc("GET /deposits", s.deposits)
	mux.HandleFunc("GET /deposits/{start_height}", s.deposits)

	mux.HandleFunc("GET /txs/recent", s.recentActivity)
	mux.HandleFunc("GET /block/{hash}/activity", s.blockActivity)

	mux.HandleFunc("GET /mempool", s.mempool)
	mux.HandleFunc("GET /mempool/txids", s.mempoolTxids)
	mux.HandleFunc("GET /mempool/recent", s.mempoolRecent)
	mux.HandleFunc("GET /fee-estimates", s.feeEstimates)

	// A sidechain lives inside the mainchain's hashrate escrow. These read that
	// escrow, so a wallet with no local node still sees which chains exist and
	// what each treasury holds.
	mux.HandleFunc("GET /drivechain/withdrawals", s.drivechainWithdrawals)
	mux.HandleFunc("GET /drivechain/sidechains", s.drivechainSidechains)
	mux.HandleFunc("GET /drivechain/sidechain/{slot}", s.drivechainSidechain)

	mux.HandleFunc("GET /health", s.health)

	return mux
}
