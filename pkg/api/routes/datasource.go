package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

func DatasetsRouter(router fiber.Router) {
	router.Get("/:identifier", getDataset)
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
