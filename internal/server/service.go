package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/joshdurbin/vector_search_poc/internal/embeddings"
	"github.com/joshdurbin/vector_search_poc/internal/store"
	pb "github.com/joshdurbin/vector_search_poc/gen"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *svc) Ingest(ctx context.Context, req *pb.IngestRequest) (*pb.IngestResponse, error) {
	if req.ProductId == "" || req.ProductName == "" || req.Description == "" {
		return nil, status.Error(codes.InvalidArgument, "product_id, product_name, and description are required")
	}

	vec, err := embeddings.Embed(ctx, s.oaiClient, s.cfg.Olmx.EmbeddingModel, req.ProductName+" "+req.Description, s.store)
	if err != nil {
		log.Error().Err(err).Msg("embed error")
		return nil, status.Error(codes.Internal, "embedding failed")
	}

	p := store.Product{
		ProductID:   req.ProductId,
		ProductName: req.ProductName,
		Category:    req.Category,
		Description: req.Description,
		Rating:      req.Rating,
		Embedding:   vec,
	}
	if err := s.store.UpsertProduct(ctx, p); err != nil {
		log.Error().Err(err).Str("product_id", req.ProductId).Msg("upsert error")
		return nil, status.Error(codes.Internal, "storage failed")
	}

	return &pb.IngestResponse{Status: "ok", ProductId: req.ProductId}, nil
}

// Load streams progress back as products are embedded and stored.
func (s *svc) Load(req *pb.LoadRequest, stream pb.ProductsService_LoadServer) error {
	ctx := stream.Context()
	send := func(p *pb.LoadProgress) error { return stream.Send(p) }

	loaded, skipped, err := s.loadCSV(ctx, bytes.NewReader(req.Content), send)
	if err != nil {
		return err
	}
	return stream.Send(&pb.LoadProgress{Done: true, Loaded: int32(loaded), Skipped: int32(skipped)})
}

func (s *svc) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	if req.Query == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}
	topN := int(req.TopN)
	if topN <= 0 {
		topN = s.cfg.Search.DefaultTopN
	}

	vec, err := embeddings.Embed(ctx, s.oaiClient, s.cfg.Olmx.EmbeddingModel, req.Query, s.store)
	if err != nil {
		log.Error().Err(err).Msg("embed error")
		return nil, status.Error(codes.Internal, "embedding failed")
	}

	if req.Not != "" {
		notVec, err := embeddings.Embed(ctx, s.oaiClient, s.cfg.Olmx.EmbeddingModel, req.Not, s.store)
		if err != nil {
			log.Error().Err(err).Msg("embed not-vector error")
			return nil, status.Error(codes.Internal, "embedding failed")
		}
		vec = store.L2Normalize(store.SubtractVectors(vec, notVec))
	}

	results, err := s.store.KNNSearch(ctx, vec, topN, req.Category, "")
	if err != nil {
		log.Error().Err(err).Msg("knn search error")
		return nil, status.Error(codes.Internal, "search failed")
	}
	return &pb.SearchResponse{Results: toProto(results), Count: int32(len(results))}, nil
}

func (s *svc) Similar(ctx context.Context, req *pb.SimilarRequest) (*pb.SearchResponse, error) {
	if req.ProductId == "" {
		return nil, status.Error(codes.InvalidArgument, "product_id is required")
	}
	topN := int(req.TopN)
	if topN <= 0 {
		topN = s.cfg.Search.DefaultTopN
	}

	vec, _, err := s.store.GetProductEmbedding(ctx, req.ProductId)
	if err != nil {
		log.Error().Err(err).Str("product_id", req.ProductId).Msg("get embedding error")
		return nil, status.Error(codes.Internal, "storage error")
	}
	if vec == nil {
		return nil, status.Error(codes.NotFound, "product not found")
	}

	results, err := s.store.KNNSearch(ctx, vec, topN, "", req.ProductId)
	if err != nil {
		log.Error().Err(err).Msg("knn search error")
		return nil, status.Error(codes.Internal, "search failed")
	}
	return &pb.SearchResponse{Results: toProto(results), Count: int32(len(results))}, nil
}

func (s *svc) Rerank(ctx context.Context, req *pb.RerankRequest) (*pb.RerankResponse, error) {
	if req.Query == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}
	topN := int(req.TopN)
	if topN <= 0 {
		topN = s.cfg.Search.DefaultTopN
	}
	rerankBy := req.RerankBy
	if rerankBy == "" {
		rerankBy = "rating"
	}
	if rerankBy != "rating" {
		return nil, status.Error(codes.InvalidArgument, "rerank_by only supports 'rating'")
	}
	if req.RerankWeight < 0 || req.RerankWeight > 1 {
		return nil, status.Error(codes.InvalidArgument, "rerank_weight must be between 0 and 1")
	}

	vec, err := embeddings.Embed(ctx, s.oaiClient, s.cfg.Olmx.EmbeddingModel, req.Query, s.store)
	if err != nil {
		log.Error().Err(err).Msg("embed error")
		return nil, status.Error(codes.Internal, "embedding failed")
	}

	candidates, err := s.store.KNNSearch(ctx, vec, s.cfg.Search.RerankPool, "", "")
	if err != nil {
		log.Error().Err(err).Msg("knn search error")
		return nil, status.Error(codes.Internal, "search failed")
	}

	minR, maxR := candidates[0].Rating, candidates[0].Rating
	for _, c := range candidates {
		if c.Rating < minR {
			minR = c.Rating
		}
		if c.Rating > maxR {
			maxR = c.Rating
		}
	}

	rr := make([]*pb.RerankResult, len(candidates))
	for i, c := range candidates {
		var norm float64
		if maxR > minR {
			norm = (c.Rating - minR) / (maxR - minR)
		}
		sim := 1 - c.Score
		rr[i] = &pb.RerankResult{
			ProductId:   c.ProductID,
			ProductName: c.ProductName,
			Category:    c.Category,
			Description: c.Description,
			Rating:      c.Rating,
			VectorScore: sim,
			FinalScore:  (1-req.RerankWeight)*sim + req.RerankWeight*norm,
		}
	}
	sort.Slice(rr, func(i, j int) bool { return rr[i].FinalScore > rr[j].FinalScore })
	if len(rr) > topN {
		rr = rr[:topN]
	}
	return &pb.RerankResponse{Results: rr, Count: int32(len(rr))}, nil
}

func (s *svc) Fusion(ctx context.Context, req *pb.FusionRequest) (*pb.SearchResponse, error) {
	if len(req.Queries) == 0 {
		return nil, status.Error(codes.InvalidArgument, "queries must not be empty")
	}
	topN := int(req.TopN)
	if topN <= 0 {
		topN = s.cfg.Search.DefaultTopN
	}

	// TODO: parallelize embedding calls
	vecs := make([][]float32, 0, len(req.Queries))
	for _, q := range req.Queries {
		vec, err := embeddings.Embed(ctx, s.oaiClient, s.cfg.Olmx.EmbeddingModel, q, s.store)
		if err != nil {
			log.Error().Err(err).Str("query", q).Msg("embed error")
			return nil, status.Error(codes.Internal, "embedding failed")
		}
		vecs = append(vecs, vec)
	}

	results, err := s.store.KNNSearch(ctx, store.AverageVectors(vecs), topN, req.Category, "")
	if err != nil {
		log.Error().Err(err).Msg("knn search error")
		return nil, status.Error(codes.Internal, "search failed")
	}
	return &pb.SearchResponse{
		Results:          toProto(results),
		Count:            int32(len(results)),
		FusionQueryCount: int32(len(req.Queries)),
	}, nil
}

func (s *svc) Range(ctx context.Context, req *pb.RangeRequest) (*pb.RangeResponse, error) {
	if req.Query == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}
	if req.MaxDistance <= 0 {
		return nil, status.Error(codes.InvalidArgument, "max_distance must be > 0")
	}
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	vec, err := embeddings.Embed(ctx, s.oaiClient, s.cfg.Olmx.EmbeddingModel, req.Query, s.store)
	if err != nil {
		log.Error().Err(err).Msg("embed error")
		return nil, status.Error(codes.Internal, "embedding failed")
	}

	results, err := s.store.RangeSearch(ctx, vec, req.MaxDistance, limit, req.Category)
	if err != nil {
		log.Error().Err(err).Msg("range search error")
		return nil, status.Error(codes.Internal, "search failed")
	}
	return &pb.RangeResponse{
		Results:     toProto(results),
		Count:       int32(len(results)),
		MaxDistance: req.MaxDistance,
	}, nil
}

func toProto(results []store.Result) []*pb.Product {
	out := make([]*pb.Product, len(results))
	for i, r := range results {
		out[i] = &pb.Product{
			ProductId:   r.ProductID,
			ProductName: r.ProductName,
			Category:    r.Category,
			Description: r.Description,
			Rating:      r.Rating,
			Score:       r.Score,
		}
	}
	return out
}

func (s *svc) loadCSV(ctx context.Context, r io.Reader, send func(*pb.LoadProgress) error) (int, int, error) {
	cr := csv.NewReader(r)
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err != nil {
		log.Error().Err(err).Msg("csv header read error")
		return 0, 0, send(&pb.LoadProgress{Done: false, Error: "failed to read CSV header"})
	}

	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	col := func(row []string, name string) string {
		i, ok := idx[name]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}

	var loaded, skipped int
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Error().Err(err).Msg("csv row error")
			skipped++
			continue
		}

		p := store.Product{
			ProductID:   col(row, "product_id"),
			ProductName: col(row, "product_name"),
			Category:    col(row, "category"),
			Description: col(row, "description"),
		}
		if rv := col(row, "rating"); rv != "" {
			p.Rating, _ = strconv.ParseFloat(rv, 64)
		}
		if p.ProductID == "" || p.ProductName == "" || p.Description == "" {
			log.Warn().Str("product_id", p.ProductID).Msg("skipping row: missing required fields")
			skipped++
			continue
		}

		vec, err := embeddings.Embed(ctx, s.oaiClient, s.cfg.Olmx.EmbeddingModel, p.ProductName+" "+p.Description, s.store)
		if err != nil {
			log.Error().Err(err).Str("product_id", p.ProductID).Msg("embed error")
			skipped++
			continue
		}
		p.Embedding = vec

		if err := s.store.UpsertProduct(ctx, p); err != nil {
			log.Error().Err(err).Str("product_id", p.ProductID).Msg("upsert error")
			skipped++
			continue
		}

		loaded++
		if loaded%50 == 0 {
			if err := send(&pb.LoadProgress{Progress: 50, TotalSoFar: int32(loaded)}); err != nil {
				return loaded, skipped, err
			}
		}
	}
	return loaded, skipped, nil
}
