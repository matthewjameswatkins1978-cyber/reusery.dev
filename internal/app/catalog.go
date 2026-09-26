package app

import (
	"context"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// MaxCapabilities bounds a capability listing. It is shared so the CLI and the
// MCP tool never disagree about how much a "list" may return.
const MaxCapabilities = 50

// Capability is one primitive together with its contract summary.
//
// Capability discovery deliberately stops here: an agent asking what Reusery
// knows must not be handed every specimen and every evidence record. The
// detail is one explicit call away.
type Capability struct {
	Primitive       model.Primitive
	ContractSummary string
}

// ListCapabilities returns at most limit capabilities ordered by primitive id.
//
// A primitive whose contract row is missing still appears; only its summary
// is unavailable, because failing the whole listing would hide a real
// capability over one broken reference.
func ListCapabilities(ctx context.Context, catalog Catalog, limit int) ([]Capability, error) {
	primitives, err := catalog.ListPrimitives(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Capability, 0, len(primitives))
	for _, primitive := range primitives {
		summary := ""
		if primitive.ContractID != "" {
			if contract, contractErr := catalog.GetContract(ctx, primitive.ContractID); contractErr == nil {
				summary = contract.Summary
			}
		}
		out = append(out, Capability{Primitive: primitive, ContractSummary: summary})
	}
	return out, nil
}
