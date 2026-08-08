package routes

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
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
		if c.Query("view") == "web" {
			reduced, marshalErr := sheriff.Marshal(&sheriff.Options{Groups: []string{"web-operator-group"}}, operatorGroup)
			if marshalErr != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": marshalErr.Error()})
			}
			return c.JSON(reduced)
		}
		if c.Query("view") != "" {
			return sheriffViewError(c, fmt.Errorf("unsupported view %q", c.Query("view")))
		}
		return c.JSON(operatorGroup)
	}
}
