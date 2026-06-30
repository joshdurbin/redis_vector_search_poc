package store

import (
	"context"
	"database/sql"
	"fmt"

	vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/joshdurbin/vector_search_poc/internal/config"
	"github.com/joshdurbin/vector_search_poc/internal/store/sqlcgen"
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog/log"
)

const knnOverfetchFactor = 4

// SQLiteStore implements Store backed by SQLite + sqlite-vec.
type SQLiteStore struct {
	db      *sql.DB
	queries *sqlcgen.Queries
	cfg     config.Config
}

// NewSQLiteStore opens (or creates) the SQLite database, loads sqlite-vec, creates
// the schema, and returns a SQLiteStore ready for use.
func NewSQLiteStore(cfg config.Config) (*SQLiteStore, error) {
	vec.Auto()

	dsn := cfg.SQLite.Path + "?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000&_cache_size=-10000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := initSQLiteSchema(db, cfg.Search.VectorDim); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	log.Info().Str("path", cfg.SQLite.Path).Msg("sqlite store ready")
	return &SQLiteStore{db: db, queries: sqlcgen.New(db), cfg: cfg}, nil
}

func initSQLiteSchema(db *sql.DB, dim int) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS products (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			product_id   TEXT UNIQUE NOT NULL,
			product_name TEXT NOT NULL,
			category     TEXT NOT NULL DEFAULT '',
			description  TEXT NOT NULL,
			rating       REAL NOT NULL DEFAULT 0
		)`,
		fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS product_vectors USING vec0(
			product_id INTEGER PRIMARY KEY,
			embedding  FLOAT[%d]
		)`, dim),
		`CREATE TABLE IF NOT EXISTS embedding_cache (
			cache_key TEXT PRIMARY KEY,
			embedding BLOB NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) UpsertProduct(ctx context.Context, p Product) error {
	id, err := s.queries.UpsertProduct(ctx, sqlcgen.UpsertProductParams{
		ProductID:   p.ProductID,
		ProductName: p.ProductName,
		Category:    p.Category,
		Description: p.Description,
		Rating:      p.Rating,
	})
	if err != nil {
		return fmt.Errorf("upsert product row: %w", err)
	}

	blob, err := vec.SerializeFloat32(p.Embedding)
	if err != nil {
		return fmt.Errorf("serialize embedding: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO product_vectors(product_id, embedding) VALUES (?, ?)`,
		id, blob,
	)
	return err
}

func (s *SQLiteStore) KNNSearch(ctx context.Context, queryVec []float32, topN int, category, excludeID string) ([]Result, error) {
	fetch := topN
	if category != "" || excludeID != "" {
		fetch = topN * knnOverfetchFactor
		if fetch < topN+1 {
			fetch = topN + 1
		}
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT p.product_id, p.product_name, p.category, p.description, p.rating, v.distance
		FROM product_vectors v
		JOIN products p ON p.id = v.product_id
		WHERE v.embedding MATCH ? AND k = ?
		ORDER BY v.distance
	`, Float32SliceToBytes(queryVec), fetch)
	if err != nil {
		return nil, fmt.Errorf("knn query: %w", err)
	}
	defer rows.Close()

	return scanAndFilter(rows, topN, category, excludeID)
}

func (s *SQLiteStore) RangeSearch(ctx context.Context, queryVec []float32, maxDist float64, limit int, category string) ([]Result, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT p.product_id, p.product_name, p.category, p.description, p.rating, v.distance
		FROM product_vectors v
		JOIN products p ON p.id = v.product_id
		WHERE v.embedding MATCH ? AND k = ? AND v.distance <= ?
		ORDER BY v.distance
	`, Float32SliceToBytes(queryVec), limit, maxDist)
	if err != nil {
		return nil, fmt.Errorf("range query: %w", err)
	}
	defer rows.Close()

	return scanAndFilter(rows, limit, category, "")
}

func (s *SQLiteStore) GetProductEmbedding(ctx context.Context, productID string) ([]float32, map[string]string, error) {
	var blob []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT v.embedding FROM product_vectors v
		JOIN products p ON p.id = v.product_id
		WHERE p.product_id = ?
	`, productID).Scan(&blob)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return BytesToFloat32Slice(blob), map[string]string{}, nil
}

func (s *SQLiteStore) GetCachedEmbedding(ctx context.Context, key string) ([]float32, bool, error) {
	b, err := s.queries.GetEmbeddingCache(ctx, key)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return BytesToFloat32Slice(b), true, nil
}

func (s *SQLiteStore) SetCachedEmbedding(ctx context.Context, key string, v []float32) error {
	return s.queries.UpsertEmbeddingCache(ctx, sqlcgen.UpsertEmbeddingCacheParams{
		CacheKey:  key,
		Embedding: Float32SliceToBytes(v),
	})
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func scanAndFilter(rows *sql.Rows, limit int, category, excludeID string) ([]Result, error) {
	var out []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.ProductID, &r.ProductName, &r.Category, &r.Description, &r.Rating, &r.Score); err != nil {
			return nil, err
		}
		if excludeID != "" && r.ProductID == excludeID {
			continue
		}
		if category != "" && r.Category != category {
			continue
		}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}
