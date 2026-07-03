package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

func DatasourcesRouter(router fiber.Router) {
	router.Get("/", listDatasources)
	router.Get("/dataset/:identifier", getDataset)
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
