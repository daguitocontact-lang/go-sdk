package daguito

import "fmt"

// maxKnowledgePathDepth caps the routing tree at two category levels
// (padre → categoria → categoria_2 → chunk). A Path with more than two
// entries is a caller error, never silently truncated.
const maxKnowledgePathDepth = 2

// expandKnowledgePath merges a positional routing Path into base under the
// reserved level keys l0, l1 (Path[i] → "l<i>"). Path wins on conflict. The
// returned map is fresh — base is never mutated — so nil base with a non-empty
// path still yields a populated map. errBase lets each call site wrap the
// failure in its own sentinel (ErrKnowledge / ErrAdmin).
func expandKnowledgePath(
	base map[string]any, path []string, errBase error,
) (map[string]any, error) {
	if len(path) > maxKnowledgePathDepth {
		return nil, fmt.Errorf(
			"%w: Path exceeds the %d-level cap (got %d entries)",
			errBase, maxKnowledgePathDepth, len(path),
		)
	}
	if len(path) == 0 {
		return base, nil
	}
	merged := make(map[string]any, len(base)+len(path))
	for k, v := range base {
		merged[k] = v
	}
	for i, level := range path {
		merged[fmt.Sprintf("l%d", i)] = level
	}
	return merged, nil
}
