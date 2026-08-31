// Package mainchain reads BIP300 state from a bip300301 enforcer.
//
// The enforcer serves its ValidatorService as Connect over plain HTTP, so a
// JSON post reads it. That keeps this module free of a protobuf toolchain.
package mainchain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// callTimeout bounds one enforcer call.
const callTimeout = 15 * time.Second

// Enforcer reads the BIP300 view of the mainchain.
type Enforcer struct {
	baseURL string
	http    *http.Client
}

// New reads through one enforcer, at its Connect address.
func New(baseURL string) *Enforcer {
	return &Enforcer{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: callTimeout},
	}
}

// URL is the enforcer this client reads.
func (e *Enforcer) URL() string { return e.baseURL }

// Sidechain is one slot of the hashrate escrow.
type Sidechain struct {
	// Slot is the sidechain number, 0 to 255.
	Slot uint32 `json:"slot"`
	// Title and Description come from the M1 proposal the slot activated with.
	Title       string `json:"title"`
	Description string `json:"description"`
	// VoteCount is how many blocks voted the proposal in.
	VoteCount        uint32 `json:"vote_count"`
	ProposalHeight   uint32 `json:"proposal_height"`
	ActivationHeight uint32 `json:"activation_height"`
	// BalanceSats is what the treasury holds, and CTIP names the output that
	// holds it. A deposit spends that output, so a wallet needs both.
	BalanceSats int64  `json:"balance_sats"`
	CtipTxid    string `json:"ctip_txid"`
	CtipVout    uint32 `json:"ctip_vout"`
}

// Sidechains lists every activated slot, in slot order.
func (e *Enforcer) Sidechains(ctx context.Context) ([]Sidechain, error) {
	var resp struct {
		Sidechains []struct {
			SidechainNumber uint32 `json:"sidechainNumber"`
			// The enforcer parses the M1 declaration for us. It reports none
			// for a slot whose declaration is not a version 0, and that slot
			// then carries no title.
			Declaration *struct {
				V0 *struct {
					Title       string `json:"title"`
					Description string `json:"description"`
				} `json:"v0"`
			} `json:"declaration"`
			VoteCount        uint32 `json:"voteCount"`
			ProposalHeight   uint32 `json:"proposalHeight"`
			ActivationHeight uint32 `json:"activationHeight"`
		} `json:"sidechains"`
	}
	if err := e.call(ctx, "GetSidechains", &resp); err != nil {
		return nil, err
	}

	out := make([]Sidechain, 0, len(resp.Sidechains))
	for _, s := range resp.Sidechains {
		chain := Sidechain{
			Slot:             s.SidechainNumber,
			VoteCount:        s.VoteCount,
			ProposalHeight:   s.ProposalHeight,
			ActivationHeight: s.ActivationHeight,
		}
		if s.Declaration != nil && s.Declaration.V0 != nil {
			chain.Title = s.Declaration.V0.Title
			chain.Description = s.Declaration.V0.Description
		}
		out = append(out, chain)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out, nil
}

// Ctip reads the treasury output of one slot: what it holds, and the outpoint
// that holds it. A deposit spends that outpoint, so a wallet needs both.
//
// A slot the enforcer knows no treasury for answers false.
func (e *Enforcer) Ctip(ctx context.Context, slot uint32) (Ctip, bool, error) {
	var resp struct {
		Ctip *struct {
			Txid struct {
				Hex string `json:"hex"`
			} `json:"txid"`
			// Connect JSON leaves out a zero, so vout is absent on the first
			// output, and an int64 arrives as a string.
			Vout  uint32 `json:"vout"`
			Value string `json:"value"`
		} `json:"ctip"`
	}
	if err := e.post(ctx, "GetCtip",
		fmt.Sprintf(`{"sidechainNumber":%d}`, slot), &resp); err != nil {
		return Ctip{}, false, err
	}
	if resp.Ctip == nil {
		return Ctip{}, false, nil
	}
	value, err := strconv.ParseInt(resp.Ctip.Value, 10, 64)
	if err != nil && resp.Ctip.Value != "" {
		return Ctip{}, false, fmt.Errorf("the treasury holds %q, which is not a number", resp.Ctip.Value)
	}
	return Ctip{Txid: resp.Ctip.Txid.Hex, Vout: resp.Ctip.Vout, ValueSats: value}, true, nil
}

// Ctip is the treasury output of one slot.
type Ctip struct {
	Txid      string `json:"txid"`
	Vout      uint32 `json:"vout"`
	ValueSats int64  `json:"value_sats"`
}

// call posts an empty request to one ValidatorService method and decodes the
// answer.
func (e *Enforcer) call(ctx context.Context, method string, out any) error {
	return e.post(ctx, method, "{}", out)
}

// post sends one ValidatorService request and decodes the answer.
func (e *Enforcer) post(ctx context.Context, method, body string, out any) error {
	url := e.baseURL + "/cusf.mainchain.v1.ValidatorService/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url,
		bytes.NewReader([]byte(body)))
	if err != nil {
		return fmt.Errorf("build the %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.http.Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", method, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read the %s answer: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d: %s", method, resp.StatusCode,
			strings.TrimSpace(string(answer)))
	}
	if err := json.Unmarshal(answer, out); err != nil {
		return fmt.Errorf("decode the %s answer: %w", method, err)
	}
	return nil
}
