package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

func readHexBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBroadcastBytes))
	if err != nil {
		return nil, fmt.Errorf("read the request body: %w", err)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(body)))
	if err != nil {
		return nil, fmt.Errorf("the body must be hex: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("the body is empty")
	}
	return raw, nil
}
