package store

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/joshdurbin/redis_vector_search_poc/internal/config"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/viterin/vek/vek32"
)

type Product struct {
	ProductID   string
	ProductName string
	Category    string
	Description string
	Rating      float64
	Embedding   []float32
}

type Result struct {
	ProductID   string
	ProductName string
	Category    string
	Description string
	Rating      float64
	Score       float64
}

func NewClient(cfg config.Config) *redis.Client {
	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.Redis.Addr,
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		UnstableResp3: true,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatal().Err(err).Msg("cannot connect to Redis")
	}
	return rdb
}

func EnsureIndex(rdb *redis.Client, cfg config.Config) error {
	dim := strconv.Itoa(cfg.Search.VectorDim)
	args := []interface{}{
		"FT.CREATE", cfg.Search.IndexName,
		"ON", "HASH",
		"PREFIX", "1", "product:",
		"SCHEMA",
		"product_id", "TAG",
		"product_name", "TEXT",
		"category", "TAG",
		"description", "TEXT",
		"rating", "NUMERIC",
		"embedding", "VECTOR", "HNSW", "10",
		"TYPE", "FLOAT32",
		"DIM", dim,
		"DISTANCE_METRIC", "COSINE",
		"M", "16",
		"EF_CONSTRUCTION", "200",
	}

	if err := rdb.Do(context.Background(), args...).Err(); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			log.Info().Str("index", cfg.Search.IndexName).Msg("index already exists, continuing")
			return nil
		}
		if strings.Contains(err.Error(), "unknown command") {
			return fmt.Errorf("Redis Stack is required but not available — start it with: docker run -d -p 6379:6379 redis/redis-stack-server:latest")
		}
		return fmt.Errorf("FT.CREATE: %w", err)
	}
	log.Info().Str("index", cfg.Search.IndexName).Msg("created index")
	return nil
}

func UpsertProduct(ctx context.Context, rdb *redis.Client, p Product) error {
	return rdb.HSet(ctx, "product:"+p.ProductID, map[string]interface{}{
		"product_id":   p.ProductID,
		"product_name": p.ProductName,
		"category":     p.Category,
		"description":  p.Description,
		"rating":       p.Rating,
		"embedding":    Float32SliceToBytes(p.Embedding),
	}).Err()
}

func KNNSearch(ctx context.Context, rdb *redis.Client, cfg config.Config, vec []float32, topN int, category, excludeID string) ([]Result, error) {
	var query string
	if category != "" {
		query = fmt.Sprintf("(@category:{%s})=>[KNN %d @embedding $vec AS __score]", EscapeTag(category), topN+1)
	} else {
		query = fmt.Sprintf("*=>[KNN %d @embedding $vec AS __score]", topN+1)
	}

	res, err := rdb.FTSearchWithArgs(ctx, cfg.Search.IndexName, query, &redis.FTSearchOptions{
		Return: []redis.FTSearchReturn{
			{FieldName: "product_id"},
			{FieldName: "product_name"},
			{FieldName: "category"},
			{FieldName: "description"},
			{FieldName: "rating"},
			{FieldName: "__score"},
		},
		Params:         map[string]interface{}{"vec": Float32SliceToBytes(vec)},
		LimitOffset:    0,
		Limit:          topN + 1,
		DialectVersion: 2,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("FT.SEARCH: %w", err)
	}
	var out []Result
	for _, doc := range res.Docs {
		if doc.ID == "product:"+excludeID {
			continue
		}
		out = append(out, docToResult(doc))
		if len(out) >= topN {
			break
		}
	}
	return out, nil
}

// RangeSearch finds products within a cosine distance threshold.
// Note: range queries on HNSW are approximate; tune EF_RUNTIME for recall vs. latency.
func RangeSearch(ctx context.Context, rdb *redis.Client, cfg config.Config, vec []float32, maxDist float64, limit int, category string) ([]Result, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	var query string
	if category != "" {
		query = fmt.Sprintf("(@category:{%s} @embedding:[VECTOR_RANGE $dist $vec])=>[KNN %d @embedding $vec AS __score]", EscapeTag(category), limit)
	} else {
		query = fmt.Sprintf("@embedding:[VECTOR_RANGE $dist $vec]=>[KNN %d @embedding $vec AS __score]", limit)
	}

	res, err := rdb.FTSearchWithArgs(ctx, cfg.Search.IndexName, query, &redis.FTSearchOptions{
		Return: []redis.FTSearchReturn{
			{FieldName: "product_id"},
			{FieldName: "product_name"},
			{FieldName: "category"},
			{FieldName: "description"},
			{FieldName: "rating"},
			{FieldName: "__score"},
		},
		Params: map[string]interface{}{
			"vec":  Float32SliceToBytes(vec),
			"dist": strconv.FormatFloat(maxDist, 'f', -1, 64),
		},
		LimitOffset:    0,
		Limit:          limit,
		DialectVersion: 2,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("FT.SEARCH range: %w", err)
	}

	out := make([]Result, len(res.Docs))
	for i, doc := range res.Docs {
		out[i] = docToResult(doc)
	}
	return out, nil
}

func docToResult(doc redis.Document) Result {
	rating, _ := strconv.ParseFloat(doc.Fields["rating"], 64)
	score, _ := strconv.ParseFloat(doc.Fields["__score"], 64)
	return Result{
		ProductID:   doc.Fields["product_id"],
		ProductName: doc.Fields["product_name"],
		Category:    doc.Fields["category"],
		Description: doc.Fields["description"],
		Rating:      rating,
		Score:       score,
	}
}

func GetProductEmbedding(ctx context.Context, rdb *redis.Client, productID string) ([]float32, map[string]string, error) {
	fields, err := rdb.HGetAll(ctx, "product:"+productID).Result()
	if err != nil {
		return nil, nil, err
	}
	if len(fields) == 0 {
		return nil, nil, nil
	}
	return BytesToFloat32Slice([]byte(fields["embedding"])), fields, nil
}


func EscapeTag(s string) string {
	special := `.,<>{}"'[]:;!@#$%^&*()-+=~`
	out := make([]byte, 0, len(s))
	for _, c := range s {
		for _, sc := range special {
			if c == sc {
				out = append(out, '\\')
				break
			}
		}
		out = append(out, byte(c))
	}
	return string(out)
}

// Vector math

func SubtractVectors(a, b []float32) []float32    { return vek32.Sub(a, b) }

func L2Normalize(a []float32) []float32 {
	if norm := vek32.Norm(a); norm != 0 {
		return vek32.DivNumber(a, norm)
	}
	return a
}

func AverageVectors(vecs [][]float32) []float32 {
	if len(vecs) == 0 {
		return nil
	}
	sum := make([]float32, len(vecs[0]))
	for _, v := range vecs {
		vek32.Add_Inplace(sum, v)
	}
	return L2Normalize(vek32.DivNumber(sum, float32(len(vecs))))
}

func Float32SliceToBytes(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func BytesToFloat32Slice(b []byte) []float32 {
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}
