package datalinker

import (
	"errors"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/insertrecords"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/urfave/cli/v2"

	"github.com/rs/zerolog/log"

	_ "time/tzdata"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "data-linker",
		Usage: "Link & merge data objects that reference the same thing",
		Subcommands: []*cli.Command{
			{
				Name:  "run",
				Usage: "Link",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "type",
						Usage:    "Type of the dataset",
						Required: true,
					},
					&cli.IntFlag{
						Name:  "max-transfer-distance-metres",
						Usage: "Maximum distance in metres for generated nearby walking transfers",
						Value: defaultStopTransferMaxDistanceMetres,
					},
					&cli.IntFlag{
						Name:  "min-change-seconds",
						Usage: "Additional change buffer applied to generated stop transfers",
						Value: defaultStopTransferMinChangeSeconds,
					},
					&cli.Float64Flag{
						Name:  "walk-speed-metres-per-second",
						Usage: "Walking speed used to calculate transfer durations",
						Value: defaultStopTransferWalkSpeedMetresPerSecond,
					},
					&cli.IntFlag{
						Name:  "batch-size",
						Usage: "Mongo bulk write batch size",
						Value: defaultStopTransferBatchSize,
					},
					&cli.IntFlag{
						Name:  "max-nearby-transfers-per-stop",
						Usage: "Maximum number of generated nearby walking transfers retained per stop",
						Value: defaultStopTransferMaxNearbyTransfers,
					},
					&cli.IntFlag{
						Name:  "stop-transfer-workers",
						Usage: "Number of workers used while discovering nearby stop transfers",
						Value: defaultStopTransferWorkerCount(),
					},
					&cli.BoolFlag{
						Name:  "skip-stop-linker-oplog-maintenance",
						Usage: "Skip post-run oplog cleanup for the stops linker",
					},
					&cli.IntFlag{
						Name:  "oplog-clear-size-mb",
						Usage: "Temporary oplog size in MB used while clearing the oplog after the stops linker",
						Value: defaultOplogClearSizeMB,
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to Redis")
					}
					insertrecords.Insert()

					dataType := c.String("type")

					switch dataType {
					case "stops":
						linker := NewLinker[*ctdf.Stop]("stop", mongo.Pipeline{
							bson.D{{Key: "$addFields", Value: bson.D{{Key: "otheridentifier", Value: "$otheridentifiers"}}}},
							bson.D{{Key: "$unwind", Value: bson.D{{Key: "path", Value: "$otheridentifier"}}}},
							bson.D{
								{
									Key: "$match",
									Value: bson.M{
										"$or": bson.A{
											// Identical values of these we will be merging by
											bson.M{"otheridentifier": bson.M{"$regex": "^gb-crs-"}},
											bson.M{"otheridentifier": bson.M{"$regex": "^gb-tiploc-"}},
											bson.M{"otheridentifier": bson.M{"$regex": "^gb-stanox-"}},
											bson.M{"otheridentifier": bson.M{"$regex": "^gb-atco-"}},
											bson.M{"otheridentifier": bson.M{"$regex": "^travigo-internalmerge-"}},
										},
									},
								},
							},
							bson.D{
								{Key: "$group",
									Value: bson.D{
										{Key: "_id", Value: "$otheridentifier"},
										{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
										{Key: "records", Value: bson.D{{Key: "$push", Value: "$$ROOT"}}},
									},
								},
							},
							bson.D{{Key: "$sort", Value: bson.D{{Key: "count", Value: -1}}}},
							bson.D{{Key: "$match", Value: bson.D{{Key: "count", Value: bson.D{{Key: "$gt", Value: 1}}}}}},
						})
						if err := linker.Run(); err != nil {
							return err
						}
						if !c.Bool("skip-stop-linker-oplog-maintenance") {
							runStopLinkerOplogMaintenance(StopLinkerMongoMaintenanceConfig{
								OplogClearSizeMB: c.Int("oplog-clear-size-mb"),
							})
						}
					case "stop-transfers", "transfers":
						return BuildStopTransfers(StopTransferBuildConfig{
							MaxDistanceMetres:         c.Int("max-transfer-distance-metres"),
							MinChangeSeconds:          c.Int("min-change-seconds"),
							WalkSpeedMetresPerSec:     c.Float64("walk-speed-metres-per-second"),
							BatchSize:                 c.Int("batch-size"),
							MaxNearbyTransfersPerStop: c.Int("max-nearby-transfers-per-stop"),
							WorkerCount:               c.Int("stop-transfer-workers"),
						})
					case "services":
						linker := NewLinker[*ctdf.Service]("service", mongo.Pipeline{
							bson.D{
								{Key: "$addFields",
									Value: bson.D{
										{Key: "servicenameoperatorconcat",
											Value: bson.D{
												{Key: "$concat",
													Value: bson.A{
														"$servicename",
														"$operatorref",
														"$transporttype",
														"$brandcolour",
													},
												},
											},
										},
									},
								},
							},
							bson.D{
								{Key: "$group",
									Value: bson.D{
										{Key: "_id", Value: "$servicenameoperatorconcat"},
										{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
										{Key: "records", Value: bson.D{{Key: "$push", Value: "$$ROOT"}}},
									},
								},
							},
							bson.D{{Key: "$sort", Value: bson.D{{Key: "count", Value: -1}}}},
							bson.D{{Key: "$match", Value: bson.D{{Key: "count", Value: bson.D{{Key: "$gt", Value: 1}}}}}},
						})
						if err := linker.Run(); err != nil {
							return err
						}
					default:
						return errors.New("Unknown type")
					}

					return nil
				},
			},
		},
	}
}
