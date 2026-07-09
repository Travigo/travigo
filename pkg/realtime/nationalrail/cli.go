package nationalrail

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/darwin"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/linxconsist"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/nrod"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/travigo/travigo/pkg/util"
	"github.com/urfave/cli/v2"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "national-rail",
		Usage: "Track GB trains using Darwin Push Port & Network Rail",
		Subcommands: []*cli.Command{
			{
				Name:  "darwin",
				Usage: "run an instance an instance of the Darwin train tracker",
				Action: func(c *cli.Context) error {
					env := util.GetEnvironmentVariables()
					if env["TRAVIGO_NATIONALRAIL_DARWIN_STOMP_USERNAME"] == "" {
						log.Fatal().Msg("TRAVIGO_NATIONALRAIL_DARWIN_STOMP_USERNAME must be set")
					}
					if env["TRAVIGO_NATIONALRAIL_DARWIN_STOMP_PASSWORD"] == "" {
						log.Fatal().Msg("TRAVIGO_NATIONALRAIL_DARWIN_STOMP_PASSWORD must be set")
					}

					if err := database.Connect(); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						return err
					}
					migrateLegacyRailDetailedAllocations(c.Context)

					log.Info().Msg("Starting National Rail Darwin Push Port train tracker")

					stompClient := darwin.StompClient{
						Address:   "darwin-dist-44ae45.nationalrail.co.uk:61613",
						Username:  env["TRAVIGO_NATIONALRAIL_DARWIN_STOMP_USERNAME"],
						Password:  env["TRAVIGO_NATIONALRAIL_DARWIN_STOMP_PASSWORD"],
						QueueName: "/topic/darwin.pushport-v16",
					}

					stompClient.Run()

					return nil
				},
			},
			{
				Name:  "linx-consist",
				Usage: "run an instance of the LINX passenger train allocation and consist tracker",
				Action: func(c *cli.Context) error {
					env := util.GetEnvironmentVariables()
					if env["TRAVIGO_LINX_CONSIST_KAFKA_TOPIC"] == "" {
						log.Fatal().Msg("TRAVIGO_LINX_CONSIST_KAFKA_TOPIC must be set")
					}
					if env["TRAVIGO_LINX_CONSIST_KAFKA_USERNAME"] == "" {
						log.Fatal().Msg("TRAVIGO_LINX_CONSIST_KAFKA_USERNAME must be set")
					}
					if env["TRAVIGO_LINX_CONSIST_KAFKA_PASSWORD"] == "" {
						log.Fatal().Msg("TRAVIGO_LINX_CONSIST_KAFKA_PASSWORD must be set")
					}

					if err := database.Connect(); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						return err
					}
					migrateLegacyRailDetailedAllocations(c.Context)

					brokers := env["TRAVIGO_LINX_CONSIST_KAFKA_BROKERS"]
					if brokers == "" {
						brokers = linxconsist.DefaultBootstrapServer
					}

					groupID := env["TRAVIGO_LINX_CONSIST_KAFKA_GROUP_ID"]
					if groupID == "" {
						groupID = "travigo-linx-consist"
					}

					log.Info().Msg("Starting LINX passenger train allocation and consist tracker")

					ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
					defer cancel()

					kafkaClient := linxconsist.KafkaClient{
						Brokers:  strings.Split(brokers, ","),
						Topic:    env["TRAVIGO_LINX_CONSIST_KAFKA_TOPIC"],
						GroupID:  groupID,
						Username: env["TRAVIGO_LINX_CONSIST_KAFKA_USERNAME"],
						Password: env["TRAVIGO_LINX_CONSIST_KAFKA_PASSWORD"],
					}

					return kafkaClient.Run(ctx)
				},
			},
			{
				Name:  "nrod",
				Usage: "run an instance an instance of the Network Rail Open Data train tracker",
				Action: func(c *cli.Context) error {
					env := util.GetEnvironmentVariables()
					if env["TRAVIGO_NETWORKRAIL_USERNAME"] == "" {
						log.Fatal().Msg("TRAVIGO_NETWORKRAIL_USERNAME must be set")
					}
					if env["TRAVIGO_NETWORKRAIL_PASSWORD"] == "" {
						log.Fatal().Msg("TRAVIGO_NETWORKRAIL_PASSWORD must be set")
					}

					if err := database.Connect(); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						return err
					}

					log.Info().Msg("Starting Network Rail Open Data train tracker")

					stompClient := nrod.StompClient{
						Address:  "publicdatafeeds.networkrail.co.uk:61618",
						Username: env["TRAVIGO_NETWORKRAIL_USERNAME"],
						Password: env["TRAVIGO_NETWORKRAIL_PASSWORD"],
					}

					stompClient.Run()

					signals := make(chan os.Signal, 1)
					signal.Notify(signals, syscall.SIGINT)
					defer signal.Stop(signals)

					<-signals // wait for signal
					go func() {
						<-signals // hard exit on second signal (in case shutdown gets stuck)
						os.Exit(1)
					}()

					// file, err := os.Open("/Users/aaronclaydon/projects/travigo/test-data/nrod.json")
					// if err != nil {
					// 	log.Fatal().Err(err).Msg("Failed to open file")
					// }
					// defer file.Close()

					// bytes, _ := io.ReadAll(file)
					// stompClient.ParseMessages(bytes)

					return nil
				},
			},
		},
	}
}

func migrateLegacyRailDetailedAllocations(ctx context.Context) {
	stats, err := realtimestore.MigrateLegacyRailDetailedAllocations(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Legacy rail allocation migration scan failed")
		return
	}

	log.Info().
		Int("scanned", stats.Scanned).
		Int("migrated", stats.Migrated).
		Int("skipped", stats.Skipped).
		Int("failed", stats.Failed).
		Msg("Legacy rail allocation migration complete")
}
