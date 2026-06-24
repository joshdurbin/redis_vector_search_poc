package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jszwec/csvutil"
	pb "github.com/joshdurbin/redis_vector_search_poc/gen"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// csvProduct is the canonical struct for a product parsed from CSV.
type csvProduct struct {
	ProductID   string   `csv:"product_id"`
	ProductName string   `csv:"product_name"`
	Category    string   `csv:"category"`
	Description string   `csv:"description"`
	Rating      *float64 `csv:"rating"`
}

// columnAliases maps WANDS column names to canonical field names.
var columnAliases = map[string]string{
	"product_class":       "category",
	"product_description": "description",
	"average_rating":      "rating",
}

func loadCmd() *cobra.Command {
	var (
		addr    string
		maxRows int
	)

	cmd := &cobra.Command{
		Use:   "load <file>",
		Short: "Load products from the WANDS product.csv into the vector store",
		Long: `Reads the WANDS product.csv file and calls Ingest over gRPC for each unique product.

Run 'make pull-data' to download the dataset before loading.

Example:
  redis-vector-search-poc load data/product.csv`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			f, err := os.Open(args[0])
			if err != nil {
				log.Fatal().Err(err).Str("file", args[0]).Msg("failed to open file")
			}
			defer f.Close()

			conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				log.Fatal().Err(err).Str("addr", addr).Msg("failed to connect to gRPC server")
			}
			defer conn.Close()
			client := pb.NewProductsServiceClient(conn)

			loaded, skipped := ingestCSV(f, client, maxRows)
			log.Info().Int("loaded", loaded).Int("skipped", skipped).Msg("load complete")
		},
	}

	cmd.Flags().StringVarP(&addr, "addr", "a", "localhost:8080", "gRPC server address")
	cmd.Flags().IntVarP(&maxRows, "rows", "n", 0, "max product rows to process (0 = unlimited)")
	return cmd
}

// normaliseHeader maps raw column names to canonical names via columnAliases.
// When two raw columns would map to the same canonical name, the first wins.
func normaliseHeader(raw []string) []string {
	out := make([]string, len(raw))
	used := make(map[string]bool)
	for i, h := range raw {
		norm := strings.ToLower(strings.TrimSpace(h))
		if canonical, ok := columnAliases[norm]; ok {
			norm = canonical
		}
		if used[norm] {
			out[i] = fmt.Sprintf("_dup_%d", i)
		} else {
			out[i] = norm
			used[norm] = true
		}
	}
	return out
}

// alignedCSVReader wraps csv.Reader and normalises every record to exactly
// nFields length so that csvutil cannot call it with an out-of-range index.
type alignedCSVReader struct {
	r       *csv.Reader
	nFields int
}

func (a *alignedCSVReader) Read() ([]string, error) {
	record, err := a.r.Read()
	if err != nil {
		return nil, err
	}
	if len(record) == a.nFields {
		return record, nil
	}
	aligned := make([]string, a.nFields)
	copy(aligned, record)
	return aligned, nil
}

func ingestCSV(r io.Reader, client pb.ProductsServiceClient, maxRows int) (int, int) {
	cr := csv.NewReader(r)
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1

	rawHeader, err := cr.Read()
	if err != nil {
		log.Error().Err(err).Msg("failed to read header")
		return 0, 0
	}

	dec, err := csvutil.NewDecoder(&alignedCSVReader{r: cr, nFields: len(rawHeader)}, normaliseHeader(rawHeader)...)
	if err != nil {
		log.Error().Err(err).Msg("failed to create csv decoder")
		return 0, 0
	}

	seen := make(map[string]bool)
	var loaded, skipped int

	for {
		if maxRows > 0 && loaded >= maxRows {
			break
		}
		var p csvProduct
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			log.Warn().Err(err).Msg("skipping malformed row")
			skipped++
			continue
		}
		if p.ProductID == "" || p.ProductName == "" || p.Description == "" {
			skipped++
			continue
		}
		if seen[p.ProductID] {
			continue
		}
		seen[p.ProductID] = true

		if err := ingestOne(client, p.ProductID, p.ProductName, p.Category, p.Description, derefRating(p.Rating)); err != nil {
			log.Error().Err(err).Str("product_id", p.ProductID).Msg("ingest failed")
			skipped++
			continue
		}
		loaded++
		if loaded%50 == 0 {
			log.Info().Int("loaded", loaded).Msg("progress")
		}
	}
	return loaded, skipped
}

func derefRating(r *float64) float64 {
	if r == nil {
		return 0
	}
	return *r
}

func ingestOne(client pb.ProductsServiceClient, productID, productName, category, description string, rating float64) error {
	_, err := client.Ingest(context.Background(), &pb.IngestRequest{
		ProductId:   productID,
		ProductName: productName,
		Category:    category,
		Description: description,
		Rating:      rating,
	})
	return err
}
