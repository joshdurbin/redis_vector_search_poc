package main

import (
	"github.com/joshdurbin/redis_vector_search_poc/internal/config"
	"github.com/joshdurbin/redis_vector_search_poc/internal/server"
	"github.com/joshdurbin/redis_vector_search_poc/internal/store"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func serveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the gRPC server",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := config.Load()
			rdb := store.NewClient(cfg)
			if err := store.EnsureIndex(rdb, cfg); err != nil {
				log.Fatal().Err(err).Msg("failed to ensure index")
			}
			server.Start(cfg, rdb)
		},
	}

	cmd.Flags().String("redis-addr", "localhost:6379", "Redis address (host:port)")
	cmd.Flags().String("redis-password", "", "Redis password")
	cmd.Flags().Int("redis-db", 0, "Redis database number")
	cmd.Flags().String("host", "", "gRPC listen host (empty = all interfaces)")
	cmd.Flags().Int("port", 8080, "gRPC listen port")
	cmd.Flags().Bool("log-requests", false, "log every gRPC request and its status code")
	cmd.Flags().String("olmx-base-url", "http://localhost:8000/v1/", "Embedding API base URL")
	cmd.Flags().String("olmx-api-key", "", "Embedding API bearer token")
	cmd.Flags().String("olmx-model", "Qwen3-Embedding-0.6B-4bit-DWQ", "Embedding model name")

	viper.BindPFlag("redis.addr", cmd.Flags().Lookup("redis-addr"))
	viper.BindPFlag("redis.password", cmd.Flags().Lookup("redis-password"))
	viper.BindPFlag("redis.db", cmd.Flags().Lookup("redis-db"))
	viper.BindPFlag("server.host", cmd.Flags().Lookup("host"))
	viper.BindPFlag("server.port", cmd.Flags().Lookup("port"))
	viper.BindPFlag("server.log_requests", cmd.Flags().Lookup("log-requests"))
	viper.BindPFlag("olmx.base_url", cmd.Flags().Lookup("olmx-base-url"))
	viper.BindPFlag("olmx.api_key", cmd.Flags().Lookup("olmx-api-key"))
	viper.BindPFlag("olmx.embedding_model", cmd.Flags().Lookup("olmx-model"))

	return cmd
}
