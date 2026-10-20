package memory

import (
	"fmt"
	"strings"
	"unicode"
)

// ValidateScopeID checks a tenant, user or agent ID. IDs are placed inside quoted
// string literals in Milvus filter expressions, so a quote, a backslash or a
// control character could end the literal and change the filter, for example to
// match or delete other tenants' vectors. Those characters are refused.
func ValidateScopeID(kind, id string) error {
	if strings.ContainsAny(id, "\"\\") {
		return fmt.Errorf("%s must not contain quotes or backslashes", kind)
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s must not contain control characters", kind)
		}
	}
	return nil
}

func validateScope(tenantID, userID, agentID string) error {
	for _, f := range []struct{ kind, id string }{{"tenant_id", tenantID}, {"user_id", userID}, {"agent_id", agentID}} {
		if err := ValidateScopeID(f.kind, f.id); err != nil {
			return err
		}
	}
	return nil
}
