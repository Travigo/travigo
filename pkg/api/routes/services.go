package routes

import (
	"context"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
)

func ServicesRouter(router fiber.Router) {
	router.Get("/", listServices)
	router.Get("/:identifier", getService)
}

func listServices(c *fiber.Ctx) error {
	c.SendString("NOT IMPLEMENTED")
	return nil
}

func getService(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	servicesCollection := database.GetCollection("services")
	var service *ctdf.Service
	servicesCollection.FindOne(context.Background(), bson.M{"primaryidentifier": identifier}).Decode(&service)

	if service == nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": "Could not find Service matching Service Identifier",
		})
	} else {
		return c.JSON(service)
	}
}
