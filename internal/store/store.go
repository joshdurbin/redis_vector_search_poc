package store

import (
	"context"
	"encoding/binary"
	"math"

	"github.com/viterin/vek/vek32"
)

// Store abstracts the vector storage backend.
type Store interface {
	// Product operations
	UpsertProduct(ctx context.Context, p Product) error
	KNNSearch(ctx context.Context, vec []float32, topN int, category, excludeID string) ([]Result, error)
	RangeSearch(ctx context.Context, vec []float32, maxDist float64, limit int, category string) ([]Result, error)
	GetProductEmbedding(ctx context.Context, productID string) ([]float32, map[string]string, error)

	// Embedding cache
	GetCachedEmbedding(ctx context.Context, key string) ([]float32, bool, error)
	SetCachedEmbedding(ctx context.Context, key string, vec []float32) error

	Close() error
}

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

func SubtractVectors(a, b []float32) []float32 { return vek32.Sub(a, b) }

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
