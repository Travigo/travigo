package routes

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func DatasourcesRouter(router fiber.Router) {
	router.Get("/", listDatasources)
	router.Get("/dataset/:identifier", getDataset)
	router.Get("/dataset/:identifier/import_report", getLatestImportReport)
	router.Get("/provider/:identifier", getProvider)
}

func listDatasources(c *fiber.Ctx) error {
	datasources, err := dataaggregator.Lookup[[]datasets.DataSource](query.DataSources{})

	if err != nil {
		c.SendStatus(500)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(datasources)
}

func getDataset(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	var dataset *datasets.DataSet
	dataset, err := dataaggregator.Lookup[*datasets.DataSet](query.DataSet{
		DataSetID: identifier,
	})

	if err != nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	} else {
		return c.JSON(dataset)
	}
}

func getLatestImportReport(c *fiber.Ctx) error {
	identifier := c.Params("identifier")
	var report datasets.DataImportReport

	err := database.GetCollection("dataset_import_reports").FindOne(
		context.Background(),
		bson.M{"datasetidentifier": identifier},
		options.FindOne().SetSort(bson.D{{Key: "creationdatetime", Value: -1}}),
	).Decode(&report)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Import report could not be found",
			})
		}

		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(report)
}

func getProvider(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	var datasource *datasets.DataSource
	datasource, err := dataaggregator.Lookup[*datasets.DataSource](query.DataSource{
		Identifier: identifier,
	})

	if err != nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	} else {
		return c.JSON(datasource)
	}
}
