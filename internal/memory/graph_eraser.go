package memory

import "context"

// GraphEraser removes from the LTM knowledge graph what Athena learned from memory
// that is being erased. Graph entries record the chains they were extracted from,
// so knowledge is removed only when no remaining chain supports it.
type GraphEraser interface {
	// EraseChains removes the given chains' contributions to the tenant and user's
	// graph and returns how many nodes and edges were deleted.
	EraseChains(ctx context.Context, tenantID, userID string, chainIDs []string) (int, error)
	// EraseScope deletes the tenant and user's whole graph, or the whole tenant's
	// when userID is empty, and returns how many nodes and edges were deleted.
	EraseScope(ctx context.Context, tenantID, userID string) (int, error)
}
