package main

import (
	"os"

	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func main() {
	var verbosity int

	root := &cobra.Command{
		Use:   "vector-search-poc",
		Short: "SQLite vector search POC",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			switch {
			case verbosity >= 2:
				zerolog.SetGlobalLevel(zerolog.TraceLevel)
			case verbosity == 1:
				zerolog.SetGlobalLevel(zerolog.DebugLevel)
			default:
				zerolog.SetGlobalLevel(zerolog.InfoLevel)
			}
		},
	}

	zlog.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	root.PersistentFlags().CountVarP(&verbosity, "verbose", "v", "log verbosity (-v debug, -vv trace)")

	root.AddCommand(serveCmd(), loadCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
