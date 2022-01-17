package routes

import (
	"context"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
)

func OperatorGroupsRouter(router fiber.Router) {
	router.Get("/:identifier", getOperatorGroup)
}

func getOperatorGroup(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	operatorGroupsCollection := database.GetCollection("operator_groups")
	var operatorGroup *ctdf.OperatorGroup
	operatorGroupsCollection.FindOne(context.Background(), bson.M{"identifier": identifier}).Decode(&operatorGroup)

	if operatorGroup == nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": "Could not find Operator Group matching Identifier",
		})
	} else {
		operatorGroup.GetReferences()
		return c.JSON(operatorGroup)
	}
}
