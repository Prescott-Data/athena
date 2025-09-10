# Azure OpenAI Migration Guide

## 🎯 Migration Summary
- **From**: Google Vertex AI (768 dimensions)
- **To**: Azure OpenAI (1536 dimensions)
- **Models**: text-embedding-ada-002 + GPT-4o

## 📝 Manual Steps Required

### 1. Create Your .env File
```bash
# Copy the template
cp .env.example .env

# Edit with your actual Azure OpenAI credentials
nano .env
```

Add your **actual API key**:
```env
AZURE_OPENAI_API_KEY=your_actual_api_key_here
```

### 2. Update Embedding Function
The `CreateEmbedding` function in `stm_store.go` needs to be updated to use Azure OpenAI instead of Google Vertex AI.

**Current**: Uses Google Vertex AI (lines 509-617)
**Needed**: Azure OpenAI embedding API call

### 3. Update LLM Function  
The `analyzeTopicContinuity` function needs to use Azure OpenAI GPT-4o instead of the current LLM endpoint.

### 4. Environment Variables Updated
- ✅ **Dimensions**: 768 → 1536 (completed)
- ✅ **Config structure**: Added Azure OpenAI vars
- ❌ **Embedding function**: Still uses Google (needs update)
- ❌ **LLM function**: Still uses generic endpoint (needs update)

## 🚧 Next Steps

1. **Replace CreateEmbedding function** with Azure OpenAI API call
2. **Replace analyzeTopicContinuity function** with Azure OpenAI GPT-4o call  
3. **Update Milvus collections** to handle 1536 dimensions
4. **Test end-to-end** with new Azure endpoints

## 📊 Changed Files
- ✅ `internal/memory/stm_store.go` (dimensions only)
- ✅ `internal/memory/milvus_client.go` (dimensions)
- ✅ `internal/memory/memory_os_e2e_test.go` (test expectations)
- ✅ `test_e2e_pipeline.sh` (documentation)
- ✅ All test files (dimension expectations)
- ❌ Embedding API implementation (pending)
- ❌ LLM API implementation (pending)

## 🔑 Secrets Management
- ✅ `.gitignore` created to exclude `.env`
- ✅ `.env.example` template created
- ⚠️ **Add your actual API key to `.env`**
