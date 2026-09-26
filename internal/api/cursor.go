package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Cursor pagination bounds the evidence inspection API.
//
// The cursor is opaque to clients: a versioned payload containing the sort key
// of the last row on the page. It never exposes a SQL offset and never uses
// numeric OFFSET pagination.
const (
	cursorVersion = 1
	// maxCursorBytes bounds accepted cursor input.
	maxCursorBytes = 2048
)

// errInvalidCursor reports a cursor that is not a Reusery v1 cursor.
var errInvalidCursor = errors.New("api: invalid cursor")

// errInvalidPagination reports an impossible page request.
var errInvalidPagination = errors.New("api: invalid pagination request")

// cursor is the decoded page position.
type cursor struct {
	Version    int       `json:"v"`
	ObservedAt time.Time `json:"o"`
	EvidenceID string    `json:"i"`
}

// encodeCursor renders an opaque cursor for the next page.
func encodeCursor(observedAt time.Time, evidenceID string) (string, error) {
	payload, err := json.Marshal(cursor{Version: cursorVersion, ObservedAt: observedAt, EvidenceID: evidenceID})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

// decodeCursor parses a client-supplied cursor.
//
// An empty cursor starts at the first page. A malformed, truncated, oversized
// or wrong-version cursor is a client problem: HTTP 422 with the
// invalid_request code, never a 500.
func decodeCursor(raw string) (cursor, error) {
	if len(raw) > maxCursorBytes {
		return cursor{}, fmt.Errorf("%w: cursor exceeds %d bytes", errInvalidPagination, maxCursorBytes)
	}
	if raw == "" {
		return cursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor{}, fmt.Errorf("%w: not a Reusery cursor", errInvalidCursor)
	}
	var decoded cursor
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return cursor{}, fmt.Errorf("%w: not a Reusery cursor", errInvalidCursor)
	}
	if decoded.Version != cursorVersion {
		return cursor{}, fmt.Errorf("%w: unsupported cursor version %d", errInvalidCursor, decoded.Version)
	}
	if strings.TrimSpace(decoded.EvidenceID) == "" {
		return cursor{}, fmt.Errorf("%w: cursor carries no evidence id", errInvalidCursor)
	}
	if decoded.ObservedAt.IsZero() {
		return cursor{}, fmt.Errorf("%w: cursor carries no observation time", errInvalidCursor)
	}
	return decoded, nil
}

// nextCursorString builds the nullable next-cursor field. It returns nil when
// the page was the last one.
func nextCursorString(observedAt time.Time, evidenceID string, hasNext bool) *string {
	if !hasNext {
		return nil
	}
	value, err := encodeCursor(observedAt, evidenceID)
	if err != nil {
		return nil
	}
	return &value
}
