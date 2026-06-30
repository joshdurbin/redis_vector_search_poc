package config

import (
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

type Config struct {
	Server ServerConfig
	SQLite SQLiteConfig
	Olmx   OlmxConfig
	Search SearchConfig
}

type ServerConfig struct {
	Host        string
	Port        int
	LogRequests bool
}

type SQLiteConfig struct {
	Path string
}

type OlmxConfig struct {
	BaseURL        string
	APIKey         string
	EmbeddingModel string
}

type SearchConfig struct {
	DefaultTopN int
	VectorDim   int
	RerankPool  int
}

func Load() Config {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	viper.SetDefault("server.host", "")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.log_requests", false)
	viper.SetDefault("sqlite.path", "products.db")
	viper.SetDefault("olmx.base_url", "http://localhost:8000/v1/")
	viper.SetDefault("olmx.embedding_model", "Qwen3-Embedding-0.6B-4bit-DWQ")
	viper.SetDefault("search.default_top_n", 5)
	viper.SetDefault("search.vector_dim", 1024)
	viper.SetDefault("search.rerank_pool", 50)

	viper.SetEnvPrefix("APP")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			log.Fatal().Err(err).Msg("error reading config file")
		}
	}

	return Config{
		Server: ServerConfig{
			Host:        viper.GetString("server.host"),
			Port:        viper.GetInt("server.port"),
			LogRequests: viper.GetBool("server.log_requests"),
		},
		SQLite: SQLiteConfig{
			Path: viper.GetString("sqlite.path"),
		},
		Olmx: OlmxConfig{
			BaseURL:        viper.GetString("olmx.base_url"),
			APIKey:         viper.GetString("olmx.api_key"),
			EmbeddingModel: viper.GetString("olmx.embedding_model"),
		},
		Search: SearchConfig{
			DefaultTopN: viper.GetInt("search.default_top_n"),
			VectorDim:   viper.GetInt("search.vector_dim"),
			RerankPool:  viper.GetInt("search.rerank_pool"),
		},
	}
}
