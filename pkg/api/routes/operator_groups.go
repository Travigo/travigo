package routes

import (
	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/dataaggregator"
	"github.com/britbus/britbus/pkg/dataaggregator/query"
	"github.com/gofiber/fiber/v2"
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
