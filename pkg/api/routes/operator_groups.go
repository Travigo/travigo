package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
)

func OperatorGroupsRouter(router fiber.Router) {
	router.Get("/:identifier", getOperatorGroup)
}

func getOperatorGroup(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	var operatorGroup *ctdf.OperatorGroup
	operatorGroup, err := dataaggregator.Lookup[*ctdf.OperatorGroup](query.OperatorGroup{
		Identifier: identifier,
	})

	if err != nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	} else {
		operatorGroup.GetReferences()
		return c.JSON(operatorGroup)
	}
}
