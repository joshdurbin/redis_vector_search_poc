package embeddings

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/joshdurbin/redis_vector_search_poc/internal/store"
	"github.com/openai/openai-go"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// Embed returns a vector for text using the provided client and model, caching
// results in Redis keyed by model+sha256(text). The client should be
// constructed once at startup and reused across calls.
func Embed(ctx context.Context, client *openai.Client, model string, text string, rdb *redis.Client) ([]float32, error) {
	cacheKey := fmt.Sprintf("emb:%s:%x", model, sha256.Sum256([]byte(text)))

	if rdb != nil {
		if b, err := rdb.Get(ctx, cacheKey).Bytes(); err == nil {
			log.Debug().Str("key", cacheKey).Msg("embedding cache hit")
			return store.BytesToFloat32Slice(b), nil
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

	if rdb != nil {
		if err := rdb.Set(ctx, cacheKey, store.Float32SliceToBytes(vec), 0).Err(); err != nil {
			log.Warn().Err(err).Str("key", cacheKey).Msg("failed to cache embedding")
		} else {
			log.Debug().Str("key", cacheKey).Msg("embedding cached")
		}
	}

	return vec, nil
}
