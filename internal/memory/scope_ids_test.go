package memory

import "testing"

func TestValidateScopeID(t *testing.T) {
	for _, ok := range []string{"acme", "alice@example.com", "room:42#private:sam", "default", ""} {
		if err := ValidateScopeID("agent_id", ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{`x" || user_id == "alice`, `x" || tenant_id != "`, `a\b`, "a\nb", "a\x00b"} {
		if err := ValidateScopeID("agent_id", bad); err == nil {
			t.Errorf("%q accepted; it can rewrite a Milvus filter", bad)
		}
	}
}

func TestMilvusFilterBuildersRefuseUnsafeScope(t *testing.T) {
	mc := &MilvusClient{} // never reached: validation fails before any call
	inj := `x" || tenant_id != "`
	if err := mc.DeleteSegmentEmbedding(nil, "acme", "alice", inj, "seg"); err == nil {
		t.Fatal("DeleteSegmentEmbedding accepted an injected agent_id")
	}
	if _, _, err := mc.SearchSimilarSegments(nil, "acme", inj, "", make([]float64, VectorDimension), 5); err == nil {
		t.Fatal("SearchSimilarSegments accepted an injected user_id")
	}
}
