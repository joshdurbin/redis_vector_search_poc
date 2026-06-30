package embeddings

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/joshdurbin/vector_search_poc/internal/store"
	"github.com/openai/openai-go"
	"github.com/rs/zerolog/log"
)

// EmbeddingCache is satisfied by the Store interface.
type EmbeddingCache interface {
	GetCachedEmbedding(ctx context.Context, key string) ([]float32, bool, error)
	SetCachedEmbedding(ctx context.Context, key string, vec []float32) error
}

// Embed returns a vector for text, caching results keyed by model+sha256(text).
func Embed(ctx context.Context, client *openai.Client, model string, text string, cache EmbeddingCache) ([]float32, error) {
	cacheKey := fmt.Sprintf("emb:%s:%x", model, sha256.Sum256([]byte(text)))

	if cache != nil {
		if vec, ok, err := cache.GetCachedEmbedding(ctx, cacheKey); err == nil && ok {
			log.Debug().Str("key", cacheKey).Msg("embedding cache hit")
			return vec, nil
		}
	}

	resp, err := client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Model: openai.EmbeddingModel(model),
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: []string{text}},
	})
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("empty embedding response")
	}

	raw := resp.Data[0].Embedding
	vec := make([]float32, len(raw))
	for i, v := range raw {
		vec[i] = float32(v)
	}

	if cache != nil {
		if err := cache.SetCachedEmbedding(ctx, cacheKey, vec); err != nil {
			log.Warn().Err(err).Str("key", cacheKey).Msg("failed to cache embedding")
		} else {
			log.Debug().Str("key", cacheKey).Msg("embedding cached")
		}
	}

	return vec, nil
}

// compile-time check: store.Store satisfies EmbeddingCache
var _ EmbeddingCache = (store.Store)(nil)
