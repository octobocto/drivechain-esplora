package mainchain

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btcd/wire"
)

// bidCacheTTL is how long one mempool scan serves every caller. A scan reads
// the whole mainchain mempool, which is megabytes, and a raise must show up in
// a few seconds. This holds both.
const bidCacheTTL = 3 * time.Second

// bidBatchSize is how many transactions one JSON-RPC batch asks for.
const bidBatchSize = 500

// coreTimeout bounds one bitcoind call.
const coreTimeout = 30 * time.Second

// Bid is one M8 BMM request the mainchain mempool holds.
type Bid struct {
	// Slot is the sidechain the bid competes for.
	Slot uint8 `json:"slot"`
	// Txid is the M8 transaction.
	Txid string `json:"txid"`
	// CriticalHash is the sidechain block the bid commits to.
	CriticalHash string `json:"critical_hash"`
	// PrevMainHash is the mainchain block the bid was built on. Only the block
	// after that one can mine the bid.
	PrevMainHash string `json:"prev_main_hash"`
	// BidSats is the fee the M8 pays, which is what the bid offers a miner.
	BidSats int64 `json:"bid_sats"`
}

// Core reads the mainchain mempool from bitcoind over JSON-RPC.
type Core struct {
	url        string
	userinfo   string
	cookiePath string
	http       *http.Client
	now        func() time.Time

	mu       sync.Mutex
	cached   []Bid
	cachedAt time.Time
}

// NewCore points a client at bitcoind. The URL takes the form
// http://host:port, and it may carry a user and a password. A URL with no user
// reads the password out of the cookie file at cookiePath.
func NewCore(rawURL, cookiePath string) (*Core, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("bitcoind address %q: %w", rawURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("bitcoind address %q needs a scheme and a host", rawURL)
	}
	userinfo := ""
	if parsed.User != nil {
		password, _ := parsed.User.Password()
		userinfo = parsed.User.Username() + ":" + password
		parsed.User = nil
	}
	return &Core{
		url:        strings.TrimRight(parsed.String(), "/"),
		userinfo:   userinfo,
		cookiePath: cookiePath,
		http:       &http.Client{Timeout: coreTimeout},
		now:        time.Now,
	}, nil
}

// URL is the bitcoind this client reads.
func (c *Core) URL() string { return c.url }

// Bids lists every M8 BMM request in the mainchain mempool, richest first. It
// serves one scan to every caller for a few seconds.
func (c *Core) Bids(ctx context.Context) ([]Bid, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cachedAt.Add(bidCacheTTL).After(c.now()) {
		return c.cached, nil
	}
	bids, err := c.scanBids(ctx)
	if err != nil {
		return nil, err
	}
	c.cached, c.cachedAt = bids, c.now()
	return bids, nil
}

// mempoolEntry is the part of a getrawmempool row a bid reads. The base fee is
// the bid.
type mempoolEntry struct {
	Fees struct {
		Base float64 `json:"base"`
	} `json:"fees"`
}

func (c *Core) scanBids(ctx context.Context) ([]Bid, error) {
	var mempool map[string]mempoolEntry
	if err := c.call(ctx, "getrawmempool", []any{true}, &mempool); err != nil {
		return nil, fmt.Errorf("read the mainchain mempool: %w", err)
	}

	txids := make([]string, 0, len(mempool))
	for txid := range mempool {
		txids = append(txids, txid)
	}
	sort.Strings(txids)

	bids := make([]Bid, 0, 8)
	for start := 0; start < len(txids); start += bidBatchSize {
		end := min(start+bidBatchSize, len(txids))
		chunk := txids[start:end]
		raws, err := c.rawTransactions(ctx, chunk)
		if err != nil {
			return nil, err
		}
		for i, raw := range raws {
			if raw == "" {
				continue
			}
			request, err := parseBidTransaction(raw)
			if err != nil {
				return nil, fmt.Errorf("read the transaction %s: %w", chunk[i], err)
			}
			if request == nil {
				continue
			}
			bids = append(bids, Bid{
				Slot:         request.Slot,
				Txid:         chunk[i],
				CriticalHash: request.CriticalHash,
				PrevMainHash: request.PrevMainHash,
				BidSats:      satsFromBTC(mempool[chunk[i]].Fees.Base),
			})
		}
	}

	sort.Slice(bids, func(i, j int) bool { return bids[i].BidSats > bids[j].BidSats })
	return bids, nil
}

// parseBidTransaction reads the M8 request off a raw transaction. It returns
// nil for every other transaction, so it can run over a whole mempool.
func parseBidTransaction(rawHex string) (*BmmRequest, error) {
	raw, err := hex.DecodeString(rawHex)
	if err != nil {
		return nil, fmt.Errorf("decode the hex: %w", err)
	}
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		return nil, fmt.Errorf("decode the transaction: %w", err)
	}
	if len(tx.TxOut) == 0 {
		return nil, nil
	}
	// A valid M8 sits at output zero, so a copy anywhere else is not a bid.
	return ParseM8BmmRequestScript(tx.TxOut[0].PkScript), nil
}

func satsFromBTC(amount float64) int64 {
	const satsPerCoin = 1e8
	if amount < 0 {
		return 0
	}
	return int64(amount*satsPerCoin + 0.5)
}

// rawTransactions reads a batch of transactions as hex, in the order asked. A
// transaction that left the mempool during the scan answers with an empty
// string.
func (c *Core) rawTransactions(ctx context.Context, txids []string) ([]string, error) {
	batch := make([]rpcRequest, 0, len(txids))
	for i, txid := range txids {
		batch = append(batch, rpcRequest{
			JSONRPC: "2.0",
			ID:      i,
			Method:  "getrawtransaction",
			Params:  []any{txid, false},
		})
	}
	body, err := json.Marshal(batch)
	if err != nil {
		return nil, fmt.Errorf("encode the transaction batch: %w", err)
	}
	answer, err := c.post(ctx, body)
	if err != nil {
		return nil, err
	}
	var replies []rpcResponse
	if err := json.Unmarshal(answer, &replies); err != nil {
		return nil, fmt.Errorf("decode the transaction batch: %w", err)
	}

	out := make([]string, len(txids))
	for _, reply := range replies {
		if reply.ID < 0 || reply.ID >= len(txids) {
			return nil, fmt.Errorf("bitcoind answered request %d, which nobody asked", reply.ID)
		}
		// The mempool moves while the scan runs, so a transaction bitcoind no
		// longer holds is not a failure.
		if reply.Error != nil {
			continue
		}
		var raw string
		if err := json.Unmarshal(reply.Result, &raw); err != nil {
			return nil, fmt.Errorf("decode the transaction %s: %w", txids[reply.ID], err)
		}
		out[reply.ID] = raw
	}
	return out, nil
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// call runs one method and decodes its result into out.
func (c *Core) call(ctx context.Context, method string, params []any, out any) error {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 0, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("encode the %s request: %w", method, err)
	}
	answer, err := c.post(ctx, body)
	if err != nil {
		return err
	}
	var reply rpcResponse
	if err := json.Unmarshal(answer, &reply); err != nil {
		return fmt.Errorf("decode the %s answer: %w", method, err)
	}
	if reply.Error != nil {
		return fmt.Errorf("%s: bitcoind error %d: %s", method, reply.Error.Code, reply.Error.Message)
	}
	if err := json.Unmarshal(reply.Result, out); err != nil {
		return fmt.Errorf("decode the %s result: %w", method, err)
	}
	return nil
}

// maxErrorBody caps how much of a non-JSON reply reaches an error message.
const maxErrorBody = 512

func (c *Core) post(ctx context.Context, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build the bitcoind request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	auth, err := c.authorization()
	if err != nil {
		return nil, err
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call bitcoind at %s: %w", c.url, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read the bitcoind answer: %w", err)
	}
	// bitcoind answers a batch with a failed item as 500, and the body still
	// holds every reply.
	if resp.StatusCode != http.StatusOK && !json.Valid(answer) {
		return nil, fmt.Errorf("bitcoind answered %d: %s", resp.StatusCode, truncate(answer))
	}
	return answer, nil
}

// authorization builds the basic auth header. bitcoind writes a new cookie at
// every start, so this reads the file at every call.
func (c *Core) authorization() (string, error) {
	userinfo := c.userinfo
	if userinfo == "" {
		if c.cookiePath == "" {
			return "", nil
		}
		raw, err := os.ReadFile(c.cookiePath)
		if err != nil {
			return "", fmt.Errorf("read the bitcoind cookie: %w", err)
		}
		userinfo = strings.TrimSpace(string(raw))
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(userinfo)), nil
}

func truncate(body []byte) string {
	if len(body) > maxErrorBody {
		return string(body[:maxErrorBody]) + "..."
	}
	return string(body)
}
