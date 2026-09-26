package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// ReadyOutput is the GET /ready response. Status is always explicit so the
// handler never emits an unset HTTP status code.
type ReadyOutput struct {
	Body   StatusResponse
	Status int
}

// handleHealth reports liveness. It never consults a readiness checker and
// never touches an external system.
func (h *Handler) handleHealth(_ context.Context, _ *struct{}) (*HealthOutput, error) {
	return &HealthOutput{Body: StatusResponse{Status: "ok"}}, nil
}

// handleReady reports readiness. Each registered checker must pass; in
// production that is PostgreSQL only. Model, GitHub, pkg.go.dev and deps.dev
// are degradable providers and are never readiness dependencies.
func (h *Handler) handleReady(ctx context.Context, _ *struct{}) (*ReadyOutput, error) {
	ctx, cancel := budget(ctx, BudgetReady)
	defer cancel()

	for _, check := range h.deps.ReadyCheckers {
		if err := check(ctx); err != nil {
			h.logger.Warn("readiness check failed", "error", truncate(err.Error(), 300))
			return &ReadyOutput{Body: StatusResponse{Status: "not ready"}, Status: http.StatusServiceUnavailable}, nil
		}
	}
	return &ReadyOutput{Body: StatusResponse{Status: "ok"}, Status: http.StatusOK}, nil
}

// documentReadyFallback adds the 503 readiness response to the generated
// contract. Huma only documents output bodies at the default status, so the
// documented failure shape is attached explicitly.
func (h *Handler) documentReadyFallback() {
	item := h.api.OpenAPI().Paths["/ready"]
	if item == nil || item.Get == nil || item.Get.Responses["200"] == nil {
		return
	}
	item.Get.Responses["503"] = &huma.Response{
		Description: http.StatusText(http.StatusServiceUnavailable),
		Content:     item.Get.Responses["200"].Content,
	}
}
