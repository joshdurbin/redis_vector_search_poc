CREATE TABLE IF NOT EXISTS products (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    product_id   TEXT UNIQUE NOT NULL,
    product_name TEXT NOT NULL,
    category     TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL,
    rating       REAL NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS embedding_cache (
    cache_key TEXT PRIMARY KEY,
    embedding BLOB NOT NULL
);
