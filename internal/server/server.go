package server

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/joshdurbin/redis_vector_search_poc/internal/config"
	"github.com/joshdurbin/redis_vector_search_poc/internal/store"
	pb "github.com/joshdurbin/redis_vector_search_poc/gen"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

func Start(cfg config.Config, rdb *redis.Client) {
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal().Err(err).Str("addr", addr).Msg("failed to listen")
	}

	oaiClient := openai.NewClient(
		option.WithAPIKey(cfg.Olmx.APIKey),
		option.WithBaseURL(cfg.Olmx.BaseURL),
	)

	var opts []grpc.ServerOption
	if cfg.Server.LogRequests {
		opts = append(opts,
			grpc.UnaryInterceptor(unaryLogger),
			grpc.StreamInterceptor(streamLogger),
		)
	}
	grpcServer := grpc.NewServer(opts...)
	pb.RegisterProductsServiceServer(grpcServer, &svc{cfg: cfg, rdb: rdb, oaiClient: &oaiClient})
	reflection.Register(grpcServer)

	log.Info().Str("addr", addr).Msg("gRPC server listening")
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatal().Err(err).Msg("server error")
	}
}

func unaryLogger(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	logRPC(info.FullMethod, time.Since(start), err)
	return resp, err
}

func streamLogger(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	start := time.Now()
	err := handler(srv, ss)
	logRPC(info.FullMethod, time.Since(start), err)
	return err
}

func logRPC(method string, dur time.Duration, err error) {
	code := codes.OK
	if err != nil {
		code = status.Code(err)
	}
	lvl := zerolog.InfoLevel
	if err != nil {
		lvl = zerolog.ErrorLevel
	}
	log.WithLevel(lvl).
		Str("method", method).
		Str("code", code.String()).
		Dur("duration", dur).
		Err(err).
		Msg("rpc")
}

// svc keeps store unexported; Start wires it to the gRPC server.
type svc struct {
	pb.UnimplementedProductsServiceServer
	cfg       config.Config
	rdb       *redis.Client
	oaiClient *openai.Client
}

// Ensure store import is used at compile time.
var _ = store.Product{}
