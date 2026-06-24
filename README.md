# Redis Vector Search POC

A gRPC server that embeds product data as vectors via an OpenAI-compatible embedding API, stores them in Redis using HNSW vector search, and exposes semantic search endpoints.

## Prerequisites

- Go 1.25+
- Redis 8+ (with Query Engine / vector search support)
- An OpenAI-compatible embedding server (defaults to `http://localhost:8000/v1/`)
- `grpcurl` for querying
- `protoc`, `protoc-gen-go`, `protoc-gen-go-grpc` (for regenerating proto code)

### Redis

Redis 8 ships vector search built-in. The Homebrew `redis` formula does not include the query engine — use Docker or `redis-stack`:

```bash
# Option A — Docker (Redis Stack, includes query engine)
docker run -d --name redis -p 6379:6379 redis/redis-stack-server:latest

# Option B — Homebrew Redis Stack
brew tap redis-stack/redis-stack
brew install redis-stack
brew services start redis-stack
```

## Build

```bash
make          # builds ./redis_vector_search_poc (default)
make proto    # regenerates Go code from products.proto
make clean    # removes binary and generated proto code
make run      # build + serve
```

## Configuration

Config is read from `config.yaml` in the working directory, or overridden by environment variables prefixed with `APP_`.

| Key | Default | Env override |
|-----|---------|-------------|
| `server.host` | `""` (all interfaces) | `APP_SERVER_HOST` |
| `server.port` | `8080` | `APP_SERVER_PORT` |
| `redis.addr` | `localhost:6379` | `APP_REDIS_ADDR` |
| `redis.password` | `""` | `APP_REDIS_PASSWORD` |
| `redis.db` | `0` | `APP_REDIS_DB` |
| `olmx.base_url` | `http://localhost:8000/v1/` | `APP_OLMX_BASE_URL` |
| `olmx.api_key` | `""` | `APP_OLMX_API_KEY` |
| `olmx.embedding_model` | `Qwen3-Embedding-0.6B-4bit-DWQ` | `APP_OLMX_EMBEDDING_MODEL` |
| `search.index_name` | `products` | `APP_SEARCH_INDEX_NAME` |
| `search.vector_dim` | `1024` | `APP_SEARCH_VECTOR_DIM` |
| `search.default_top_n` | `5` | `APP_SEARCH_DEFAULT_TOP_N` |
| `search.rerank_pool` | `50` | `APP_SEARCH_RERANK_POOL` |

> `vector_dim` must match the output dimension of your embedding model.

Example `config.yaml`:

```yaml
server:
  port: 8080
redis:
  addr: "localhost:6379"
olmx:
  base_url: "http://localhost:8000/v1/"
  api_key: "your-key"
  embedding_model: "Qwen3-Embedding-0.6B-4bit-DWQ"
search:
  vector_dim: 1024
```

## Starting the server

```bash
./redis_vector_search_poc serve

# All flags (override config/env)
./redis_vector_search_poc serve \
  --redis-addr localhost:6379 \
  --olmx-base-url http://localhost:8000/v1/ \
  --olmx-api-key your-key \
  --olmx-model Qwen3-Embedding-0.6B-4bit-DWQ \
  --host "" \
  --port 8080 \
  -v        # debug logging
  -vv       # trace logging
```

The server creates the Redis HNSW index on startup if it does not already exist, then begins serving gRPC on the configured address.

## Loading data

The `load` subcommand reads a local file or downloads from a URL, embeds each product via the running server, and stores it in Redis. The server must be running.

```bash
# Local CSV (WANDS Wayfair dataset — tab-separated despite .csv extension)
./redis_vector_search_poc load data/product.csv

# Local JSON array
./redis_vector_search_poc load data/products.json

# Remote gzip-compressed JSONL
./redis_vector_search_poc load https://example.com/products.jsonl.gz

# Limit rows and target a non-default server
./redis_vector_search_poc load data/product.csv --rows 500 --addr localhost:8080
```

**Supported formats** (auto-detected from extension, delimiter auto-detected from content):

| Extension | Format |
|-----------|--------|
| `.csv`, `.tsv` | Delimited — delimiter sniffed from first line |
| `.json` | JSON array `[{…}, …]` |
| `.jsonl` | Newline-delimited JSON |
| `.gz` | Any of the above, gzip-compressed |

**Column name mapping** — these naming conventions all work without pre-processing:

| Canonical | WANDS (Wayfair) | Amazon reviews |
|-----------|----------------|----------------|
| `product_id` | `product_id` | `product_id` / `asin` |
| `product_name` | `product_name` | `product_title` |
| `category` | `product_class` | `product_category` |
| `description` | `product_description` | `review_body` |
| `rating` | `average_rating` | `star_rating` |

## Querying with grpcurl

The server registers gRPC reflection, so `grpcurl` discovers the schema automatically — no proto file needed locally.

```bash
# List all available RPCs
grpcurl -plaintext localhost:8080 describe products.ProductsService
```

---

### Ingest — add a single product

Embeds the product and stores it in Redis.

```bash
grpcurl -plaintext -d '{
  "product_id":   "abc123",
  "product_name": "Ergonomic Mesh Chair",
  "category":     "Office Chairs",
  "description":  "Adjustable lumbar support, breathable mesh back, 5-year warranty.",
  "rating":       4.7
}' localhost:8080 products.ProductsService/Ingest
```

```json
{"status": "ok", "productId": "abc123"}
```

---

### Search — semantic KNN search

Embeds the query and returns the K nearest products from the HNSW index.

```bash
# Basic search
grpcurl -plaintext -d '{
  "query":  "comfortable chair for back pain",
  "top_n":  5
}' localhost:8080 products.ProductsService/Search

# With category filter (TAG match on the Redis index)
grpcurl -plaintext -d '{
  "query":    "wooden bed frame",
  "top_n":    10,
  "category": "Beds"
}' localhost:8080 products.ProductsService/Search

# With negative vector — results similar to query but dissimilar to "not"
grpcurl -plaintext -d '{
  "query":  "office desk",
  "top_n":  5,
  "not":    "cheap plastic flimsy"
}' localhost:8080 products.ProductsService/Search

# Category filter + negative vector
grpcurl -plaintext -d '{
  "query":    "storage solution",
  "top_n":    8,
  "category": "Shelving",
  "not":      "wall mounted"
}' localhost:8080 products.ProductsService/Search
```

Response shape:

```json
{
  "results": [
    {
      "productId":   "1502",
      "productName": "Julie Solid Wood Sleigh Bed",
      "category":    "Beds",
      "description": "Solid pine wood from Vietnam…",
      "rating":      4.0,
      "score":       0.142
    }
  ],
  "count": 5
}
```

`score` is cosine distance — lower is more similar (0 = identical, 1 = opposite).

---

### Similar — find products like a known product

Fetches the stored embedding for `product_id` and runs KNN with it as the query. The source product is excluded from results.

```bash
grpcurl -plaintext -d '{
  "product_id": "1502",
  "top_n":      5
}' localhost:8080 products.ProductsService/Similar
```

Returns the same shape as Search.

---

### Rerank — blend vector score with a numeric field

Fetches a larger candidate pool (`search.rerank_pool`, default 50), then re-ranks by blending cosine similarity with a normalised field value.

```bash
grpcurl -plaintext -d '{
  "query":         "standing desk with cable management",
  "top_n":         10,
  "rerank_by":     "rating",
  "rerank_weight": 0.3
}' localhost:8080 products.ProductsService/Rerank
```

`rerank_weight` is between 0 and 1:
- `0.0` — pure vector similarity (same as Search)
- `1.0` — pure rating
- `0.3` — 70% vector, 30% rating

Response:

```json
{
  "results": [
    {
      "productId":   "1069",
      "productName": "Samuels Upholstered Standard Bed",
      "category":    "Beds",
      "description": "…",
      "rating":      5.0,
      "vectorScore": 0.85,
      "finalScore":  0.91
    }
  ],
  "count": 10
}
```

---

### Fusion — average multiple query embeddings

Embeds each query independently, averages the vectors (L2-normalised), then runs a single KNN search. Useful for combining concepts.

```bash
grpcurl -plaintext -d '{
  "queries":  ["standing desk", "ergonomic chair", "monitor arm"],
  "top_n":    10,
  "category": "Office Furniture"
}' localhost:8080 products.ProductsService/Fusion
```

Response is the same shape as Search with an additional field:

```json
{
  "results": [...],
  "count": 10,
  "fusionQueryCount": 3
}
```

---

### Range — return all products within a distance threshold

Uses Redis `VECTOR_RANGE` to find every product within `max_distance` cosine distance of the query. Results are approximate (HNSW).

```bash
grpcurl -plaintext -d '{
  "query":        "noise cancelling headphones",
  "max_distance": 0.25,
  "limit":        100
}' localhost:8080 products.ProductsService/Range

# With category filter
grpcurl -plaintext -d '{
  "query":        "wireless speaker",
  "max_distance": 0.20,
  "limit":        50,
  "category":     "Electronics"
}' localhost:8080 products.ProductsService/Range
```

`max_distance` is cosine distance (0.0 = identical, 1.0 = opposite). `limit` caps results (max 500, default 100).

Response:

```json
{
  "results": [...],
  "count": 34,
  "maxDistance": 0.25
}
```

---

### Load (streaming RPC) — bulk load via raw file bytes

Intended for server-side loading where the client sends raw file content. The `load` CLI subcommand is the preferred interface for this; the RPC is available for other clients.

```bash
# Send a local JSON file via grpcurl
grpcurl -plaintext -d "{
  \"content\": \"$(base64 < data/products.json)\",
  \"format\":  \"json\"
}" localhost:8080 products.ProductsService/Load
```

The server streams back progress lines:

```json
{"progress": 50, "totalSoFar": 50}
{"progress": 50, "totalSoFar": 100}
{"done": true, "loaded": 194, "skipped": 0}
```

## Project structure

```
cmd/redis-vector-search-poc/
  main.go       — root cobra command, zerolog setup
  serve.go      — serve subcommand + flag binding
  load.go       — load subcommand, file/URL parsing, CSV/TSV/JSON ingestion
internal/
  config/       — Config structs, viper loading
  embeddings/   — Embed() with Redis cache (key: emb:{model}:{sha256})
  store/        — Redis client, HNSW index, KNN/range search, vector math
  server/
    server.go   — gRPC server, request/response interceptors
    service.go  — all RPC implementations
gen/            — protobuf-generated Go (do not edit)
products.proto  — service and message definitions
Makefile
```

## Embedding cache

Identical text+model combinations are cached in Redis as raw `FLOAT32` bytes with no TTL:

```
key: emb:{model}:{sha256(text)}
```

Cache hits are logged at debug level (`-v`). To clear the cache:

```bash
redis-cli --scan --pattern "emb:*" | xargs redis-cli del
```
