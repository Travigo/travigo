package datalinker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/insertrecords"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/travigo/travigo/pkg/stopservices"
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
				Name:  "stop-services",
				Usage: "Build the materialized stop-services collection",
				Subcommands: []*cli.Command{
					{
						Name:  "build",
						Usage: "Build the complete static stop-services view, or one dataset",
						Flags: []cli.Flag{
							&cli.StringFlag{Name: "dataset", Usage: "Build only this dataset's contribution"},
							&cli.IntFlag{Name: "batch-size", Value: stopservices.DefaultBatchSize, Usage: "Maximum stop-service rows buffered per Mongo bulk write"},
							&cli.DurationFlag{Name: "timeout", Value: 90 * time.Minute, Usage: "Maximum build duration"},
						},
						Action: func(c *cli.Context) error {
							return runStopServicesCommand(c, func(ctx context.Context, db *mongo.Database) error {
								buildOptions := stopservices.BuildOptions{BatchSize: c.Int("batch-size")}
								if datasetID := c.String("dataset"); datasetID != "" {
									return stopservices.BuildDatasetWithOptions(ctx, db, datasetID, buildOptions)
								}
								return stopservices.BuildWithOptions(ctx, db, buildOptions)
							})
						},
					},
					{
						Name:  "pending",
						Usage: "Refresh dirty datasets without rebuilding the complete view",
						Flags: []cli.Flag{
							&cli.DurationFlag{Name: "timeout", Value: 90 * time.Minute, Usage: "Maximum refresh duration"},
						},
						Action: func(c *cli.Context) error {
							return runStopServicesCommand(c, func(ctx context.Context, db *mongo.Database) error {
								built, err := stopservices.BuildPending(ctx, db)
								if errors.Is(err, stopservices.ErrUnbuilt) {
									log.Info().Msg("Stop-services materialization has not been bootstrapped; skipping pending refresh")
									return nil
								}
								if err != nil {
									return err
								}
								log.Info().Int("datasets", built).Msg("Refreshed pending stop-services datasets")
								return nil
							})
						},
					},
					{
						Name:  "recover-writers",
						Usage: "Clear source-write markers left by a stopped publisher",
						Flags: []cli.Flag{
							&cli.BoolFlag{Name: "confirm-no-publishers", Required: true, Usage: "Confirm every journey publisher has stopped"},
							&cli.DurationFlag{Name: "timeout", Value: 30 * time.Second, Usage: "Maximum recovery duration"},
						},
						Action: func(c *cli.Context) error {
							if !c.Bool("confirm-no-publishers") {
								return fmt.Errorf("--confirm-no-publishers must be true after stopping all journey publishers")
							}
							return runStopServicesCommand(c, func(ctx context.Context, db *mongo.Database) error {
								if err := stopservices.RecoverSourceWriters(ctx, db); err != nil {
									return err
								}
								log.Warn().Msg("Recovered stop-services source markers; run pending refresh to rebuild affected datasets")
								return nil
							})
						},
					},
				},
			},
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
					&cli.BoolFlag{
						Name:  "skip-staging",
						Usage: "Incrementally publish changed journey datasets with per-dataset checkpoints",
					},
					&cli.IntFlag{
						Name:  "oplog-clear-size-mb",
						Usage: "Temporary oplog size in MB used while clearing the oplog after the stops linker",
						Value: defaultOplogClearSizeMB,
					},
				},
				Action: func(c *cli.Context) error {
					dataType := c.String("type")
					if c.Bool("skip-staging") && dataType != "journeys" {
						return fmt.Errorf("--skip-staging is only supported for journeys")
					}

					if err := database.Connect(); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to Redis")
					}
					insertrecords.Insert()

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
					case "journeys":
						linker := NewPlainCopyLinker("journey")
						if err := linker.Run(PlainCopyConfig{SkipStaging: c.Bool("skip-staging")}); err != nil {
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

func runStopServicesCommand(c *cli.Context, run func(context.Context, *mongo.Database) error) error {
	if err := database.Connect(); err != nil {
		return err
	}

	ctx := c.Context
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := c.Duration("timeout")
	if timeout <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := stopservices.EnsureSchema(ctx, database.Instance.Database); err != nil {
		return fmt.Errorf("ensure stop-services schema: %w", err)
	}
	return run(ctx, database.Instance.Database)
}
