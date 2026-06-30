-- name: UpsertProduct :one
INSERT INTO products (product_id, product_name, category, description, rating)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(product_id) DO UPDATE SET
    product_name = excluded.product_name,
    category     = excluded.category,
    description  = excluded.description,
    rating       = excluded.rating
RETURNING id;

-- name: GetProductByProductID :one
SELECT id, product_id, product_name, category, description, rating
FROM products WHERE product_id = ?;

-- name: UpsertEmbeddingCache :exec
INSERT OR REPLACE INTO embedding_cache (cache_key, embedding) VALUES (?, ?);

-- name: GetEmbeddingCache :one
SELECT embedding FROM embedding_cache WHERE cache_key = ?;
