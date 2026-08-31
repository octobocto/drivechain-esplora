package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// maxBroadcastBytes caps a POST /tx body.
const maxBroadcastBytes = 1 << 20

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status is already on the wire, so the only report left is a log
		// line the caller writes.
		return
	}
}

func writeText(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, body)
}

// writeError answers in plain text, because an Esplora client reads the body of
// a failure as a message rather than as JSON.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, message)
}

// readTransactionBody reads the authorized transaction a POST /tx carries.
//
// Bitcoin Esplora takes a hex raw transaction here. These chains have none: a
// node reads an authorized transaction as JSON, and only ten of its types can
// borsh-decode. So the body is that JSON, and this service relays it.
func readTransactionBody(r *http.Request) (json.RawMessage, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBroadcastBytes))
	if err != nil {
		return nil, fmt.Errorf("read the request body: %w", err)
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("the body is empty")
	}
	if !json.Valid(trimmed) {
		return nil, fmt.Errorf("the body must be the transaction as JSON")
	}
	return json.RawMessage(trimmed), nil
}
