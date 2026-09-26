package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

// ServeHTTP applies the cross-cutting request contract and then dispatches.
//
// It is deliberately small: request correlation, safe response headers and
// one bounded structured log line. There is no CORS policy in Packet 8 and no
// authentication: both arrive with later packets and neither is pretended here.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()

	recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
	requestID := normaliseRequestID(r.Header.Get(headerRequestID))

	header := recorder.Header()
	header.Set(headerRequestID, requestID)
	header.Set(headerAPIVersion, APIVersion)
	header.Set(headerNoStore, "no-store")
	header.Set(headerNoSniff, "nosniff")

	r.Header.Set(headerRequestID, requestID)

	defer func() {
		if recovered := recover(); recovered != nil {
			h.logger.Error("request panic",
				"request_id", requestID,
				"operation_id", h.operationIDFor(r.Method, r.URL.Path),
				"method", r.Method,
				"error", recoveredValue(recovered))
			if !recorder.wroteHeader {
				writeProblem(recorder, problem(http.StatusInternalServerError, CodeInternalError,
					"the request could not be completed"))
			}
		}
	}()

	h.mux.ServeHTTP(recorder, r)

	h.logger.Info("http request",
		"request_id", requestID,
		"operation_id", h.operationIDFor(r.Method, r.URL.Path),
		"method", r.Method,
		"status", recorder.status,
		"duration_ms", time.Since(started).Milliseconds())
}

// recoveredValue renders a panic without a stack trace or any request data.
func recoveredValue(value any) string {
	if err, ok := value.(error); ok {
		return err.Error()
	}
	return "panic"
}

// writeProblem writes an RFC 9457 body onto a raw response writer. It is only
// used by the panic path, where no Huma context exists any more.
func writeProblem(w http.ResponseWriter, p *Problem) {
	body, err := json.Marshal(p)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	if _, err := w.Write(body); err != nil {
		return
	}
}

// responseRecorder captures the status code for structured logging.
type responseRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(body []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(body)
}

// normaliseRequestID validates an inbound correlation id.
//
// A missing, oversized or unsafe value is replaced by a generated one rather
// than rejected: correlation must never fail a request, and no security
// decision is ever derived from it.
func normaliseRequestID(raw string) string {
	if isSafeRequestID(raw) {
		return raw
	}
	return newRequestID()
}

func isSafeRequestID(raw string) bool {
	if raw == "" || len(raw) > maxRequestIDLength {
		return false
	}
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == ':':
		default:
			return false
		}
	}
	return true
}

// newRequestID mints a correlation id from crypto/rand. It is not a UUID and
// deliberately pulls in no UUID dependency.
func newRequestID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "unidentified"
	}
	return hex.EncodeToString(buf)
}

// budget wraps a service call in an explicit per-operation deadline. The
// tighter inner deadline of a provider or service always wins.
func budget(ctx context.Context, limit time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, limit)
}

// opError maps a service failure onto the API error contract, preferring the
// operation budget when that is the real cause of the failure.
func opError(ctx context.Context, err error) *Problem {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return classify(ctxErr)
	}
	return classify(err)
}

// externalOperationsGate reports the shared 503 for operations that spend
// model tokens or external provider quota while the switch is off.
func externalOperationsGate(enabled bool) *Problem {
	if enabled {
		return nil
	}
	return classify(errExternalOperations)
}

// truncate bounds a string used in a response or log.
func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
