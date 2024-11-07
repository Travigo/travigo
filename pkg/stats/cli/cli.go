package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/stats/calculator"
	"github.com/travigo/travigo/pkg/stats/web_api"
	"github.com/urfave/cli/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type RecordStatsData struct {
	Type      string
	Stats     interface{}
	Timestamp time.Time
}

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "stats",
		Usage: "Provides indexing & statistics API endpoints",
		Subcommands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run stats server",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "listen",
						Value: ":8081",
						Usage: "listen target for the web server",
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := elastic_client.Connect(true); err != nil {
						return err
					}

					web_api.SetupServer(c.String("listen"))

					return nil
				},
			},
			{
				Name:  "calculate",
				Usage: "calculate stats for an object",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "object",
						Usage:    "Name of the object",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := elastic_client.Connect(false); err != nil {
						return err
					}

					objectNames := strings.Split(c.String("object"), ",")

					for _, objectName := range objectNames {
						log.Info().Str("type", objectName).Msg("Updating stats")

						var statsData interface{}

						switch objectName {
						case "services":
							statsData = calculator.GetServices()
						case "operators":
							statsData = calculator.GetOperators()
						case "stops":
							statsData = calculator.GetStops()
						case "servicealerts":
							statsData = calculator.GetServiceAlerts()
						case "realtimejourneys":
							statsData = calculator.GetRealtimeJourneys()
						default:
							log.Error().Str("type", objectName).Msg("Unknown type")
							continue
						}

						log.Info().Str("type", objectName).Interface("stats", statsData).Msg("Calculated")

						recordStatsData := RecordStatsData{
							Type:      objectName,
							Stats:     statsData,
							Timestamp: time.Now(),
						}

						// Add to Mongo
						statsCollection := database.GetCollection("stats")
						statsCollection.UpdateOne(context.Background(), bson.M{"type": objectName}, bson.M{"$set": recordStatsData}, options.Update().SetUpsert(true))

						// Publish stats to Elasticsearch
						elasticEvent, _ := json.Marshal(recordStatsData)
						elastic_client.IndexRequest("overall-stats-1", bytes.NewReader(elasticEvent))
					}

					return nil
				},
			},
		},
	}
}
