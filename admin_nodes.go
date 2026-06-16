package daguito

import (
	"context"
	"encoding/json"
	"fmt"
)

// NodesService reads the flow-builder node catalog. Backed by
// GET /api/public/nodes/catalog — no auth required, but the SDK still
// sends the bearer to match the rest of the surface.
type NodesService struct {
	transport *adminTransport
}

// NodeCatalog is the raw catalog the server returns. Its shape evolves
// frequently (new node kinds, new descriptors), so the SDK keeps it as a
// pass-through map — callers index by `step_type` and read whatever fields
// they need without coupling to a frozen Go struct.
type NodeCatalog = map[string]any

// GetCatalog returns the full node catalog as a generic map.
func (s *NodesService) GetCatalog(ctx context.Context) (NodeCatalog, error) {
	raw, err := s.transport.requestJSON(ctx, "GET", "/api/public/nodes/catalog", nil)
	if err != nil {
		return nil, err
	}
	out := NodeCatalog{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return out, nil
}
