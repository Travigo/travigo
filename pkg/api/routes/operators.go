package routes

import (
	"context"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
)

func OperatorsRouter(router fiber.Router) {
	router.Get("/", listOperators)
	router.Get("/:identifier", getOperator)
}

func listOperators(c *fiber.Ctx) error {
	c.SendString("NOT IMPLEMENTED")
	return nil
}

func getOperator(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	operatorsCollection := database.GetCollection("operators")
	var operator *ctdf.Operator
	operatorsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": identifier}).Decode(&operator)

	if operator == nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": "Could not find Operator matching Operator Identifier",
		})
	} else {
		operator.GetReferences()
		return c.JSON(operator)
	}
}
