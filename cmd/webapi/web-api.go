package main

import (
	"log"
	"os"

	"github.com/britbus/britbus/pkg/api"
	"github.com/britbus/britbus/pkg/database"
	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name: "web-api",
		Commands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run web api server",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "listen",
						Value: ":8080",
						Usage: "lsiten target for the web server",
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						log.Fatal(err)
					}

					api.SetupServer(c.String("listen"))

					return nil
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
