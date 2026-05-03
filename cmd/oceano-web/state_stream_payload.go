package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// streamWantsVU reports whether the client requests stereo meter fields in SSE/state JSON.
// Default is false (omit `vu`) so mobile clients avoid high-frequency decode churn; pass vu=1
// for HDMI / nowplaying.html (see static/nowplaying/main.js).
func streamWantsVU(r *http.Request) bool {
	return strings.TrimSpace(r.URL.Query().Get("vu")) == "1"
}

// rewriteStateJSONForClient removes top-level "vu" when includeVU is false.
// Unknown fields are preserved via json.RawMessage map round-trip.
func rewriteStateJSONForClient(raw []byte, includeVU bool) ([]byte, error) {
	if includeVU {
		return raw, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw, err
	}
	delete(m, "vu")
	return json.Marshal(m)
}

// formatSSEEvent writes an optional named SSE event followed by a data frame.
func formatSSEEvent(eventName string, data []byte) string {
	var b strings.Builder
	if strings.TrimSpace(eventName) != "" {
		b.WriteString("event: ")
		b.WriteString(strings.TrimSpace(eventName))
		b.WriteByte('\n')
	}
	b.WriteString(formatSSEDataFrame(data))
	return b.String()
}
