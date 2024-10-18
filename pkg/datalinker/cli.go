package datalinker

import (
	"errors"

	"github.com/travigo/travigo/pkg/dataimporter/insertrecords"

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

					if dataType == "stop" {
						StopExample()
					} else {
						return errors.New("Unknown type")
					}

					return nil
				},
			},
		},
	}
}
