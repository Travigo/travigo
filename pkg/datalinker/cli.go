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
							// bson.D{
							// 	{
							// 		Key: "$match",
							// 		Value: bson.M{
							// 			"$or": bson.A{
							// 				// Identical values of these we will be merging by
							// 				bson.M{"otheridentifier": bson.M{"$regex": "^gb-crs-"}},
							// 				bson.M{"otheridentifier": bson.M{"$regex": "^gb-tiploc-"}},
							// 				bson.M{"otheridentifier": bson.M{"$regex": "^gb-stanox-"}},
							// 				bson.M{"otheridentifier": bson.M{"$regex": "^gb-atco-"}},
							// 				bson.M{"otheridentifier": bson.M{"$regex": "^travigo-internalmerge-"}},
							// 			},
							// 		},
							// 	},
							// },
							bson.D{
								{Key: "$group",
									Value: bson.D{
										{Key: "_id", Value: "$otheridentifier"},
										{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
										{Key: "records", Value: bson.D{{Key: "$push", Value: "$$ROOT"}}},
									},
								},
							},
							// bson.D{{Key: "$sort", Value: bson.D{{Key: "count", Value: -1}}}},
							// bson.D{{Key: "$match", Value: bson.D{{Key: "count", Value: bson.D{{Key: "$gt", Value: 1}}}}}},
						})
						linker.Run()
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
						linker.Run()
					default:
						return errors.New("Unknown type")
					}

					return nil
				},
			},
		},
	}
}
