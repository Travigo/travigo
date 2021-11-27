package main

import (
	"errors"
	"log"
	"os"

	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/naptan"
	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name: "data-importer",
		Commands: []*cli.Command{
			{
				Name:  "naptan",
				Usage: "NaPTAN stop location data",
				Subcommands: []*cli.Command{
					{
						Name:      "import-file",
						Usage:     "import an XML file",
						ArgsUsage: "<file path>",
						Action: func(c *cli.Context) error {
							if c.Args().Len() == 0 {
								return errors.New("file path must be provided")
							}

							if err := database.Connect(); err != nil {
								log.Fatal(err)
							}

							filePath := c.Args().Get(0)
							naptanDoc, err := naptan.ParseXMLFile(filePath, naptan.BusFilter)

							if err != nil {
								return err
							}

							naptanDoc.ImportIntoMongo()

							return nil
						},
					},
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
