package departuregraph

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/util"
	"github.com/urfave/cli/v2"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "departure-graph",
		Usage: "Serve the rolling scheduled-departure graph",
		Subcommands: []*cli.Command{{
			Name:  "run",
			Usage: "Run the departure graph service",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "listen", Value: ":8080", Usage: "HTTP listen address"},
			},
			Action: runService,
		}},
	}
}

func runService(c *cli.Context) error {
	if err := database.Connect(); err != nil {
		return err
	}

	config := ConfigFromEnvironment(util.GetEnvironmentVariables())
	config.Enabled = true
	serviceContext, stop := signal.NotifyContext(c.Context, os.Interrupt, syscall.SIGTERM)
	defer stop()

	graph := New(MongoLoader{}, config)
	graph.Start(serviceContext)
	server := &http.Server{
		Addr:              c.String("listen"),
		Handler:           NewServer(graph).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	result := make(chan error, 1)
	go func() {
		log.Info().Str("listen", server.Addr).Msg("Starting departure graph service")
		result <- server.ListenAndServe()
	}()

	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-serviceContext.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownContext)
		if err := graph.Save(); err != nil {
			log.Error().Err(err).Msg("Final departure graph snapshot save failed")
		}
		return shutdownErr
	}
}
