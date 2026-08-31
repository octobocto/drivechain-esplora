// Package rpc talks to a rust sidechain node over JSON-RPC 2.0. Every rust
// sidechain speaks the same protocol, so one client serves them all.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultTimeout bounds one node call. A block body on a large chain takes a
// while to serialize, so this is generous.
const DefaultTimeout = 60 * time.Second

// Client is a JSON-RPC 2.0 client for one sidechain node.
type Client struct {
	url    string
	http   *http.Client
	nextID atomic.Int64
}

// New points a client at a node. The URL takes the form http://host:port.
func New(url string) *Client {
	return &Client{
		url:  strings.TrimRight(url, "/"),
		http: &http.Client{Timeout: DefaultTimeout},
	}
}

// NewWithHTTPClient points a client at a node with a caller-supplied
// http.Client, for a proxy or a different timeout.
func NewWithHTTPClient(url string, httpClient *http.Client) *Client {
	return &Client{url: strings.TrimRight(url, "/"), http: httpClient}
}

// URL is the node address this client dials.
func (c *Client) URL() string { return c.url }

// Error is a JSON-RPC error the node returned. The node reports a missing block
// body this way rather than as an absent result, so callers read Code.
type Error struct {
	Method  string
	Code    int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: node error %d: %s", e.Method, e.Code, e.Message)
}

// HTTPError is a transport failure. The node never answered with JSON-RPC.
type HTTPError struct {
	Method string
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: node returned HTTP %d: %s", e.Method, e.Status, e.Body)
}

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type response struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// maxErrorBody caps how much of a non-JSON reply reaches an error message.
const maxErrorBody = 512

// Call runs one method and decodes its result into out. Pass a nil out to
// discard the result.
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	body, err := json.Marshal(request{
		JSONRPC: "2.0",
		ID:      c.nextID.Add(1),
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return fmt.Errorf("%s: encode request: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: build request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: call node at %s: %w", method, c.url, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: read response: %w", method, err)
	}

	var parsed response
	if err := json.Unmarshal(raw, &parsed); err != nil {
		if resp.StatusCode != http.StatusOK {
			return &HTTPError{Method: method, Status: resp.StatusCode, Body: truncate(raw)}
		}
		return fmt.Errorf("%s: decode response: %w", method, err)
	}
	if parsed.Error != nil {
		return &Error{Method: method, Code: parsed.Error.Code, Message: parsed.Error.Message}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(parsed.Result, out); err != nil {
		return fmt.Errorf("%s: decode result: %w", method, err)
	}
	return nil
}

func truncate(body []byte) string {
	if len(body) > maxErrorBody {
		return string(body[:maxErrorBody]) + "..."
	}
	return string(body)
}
