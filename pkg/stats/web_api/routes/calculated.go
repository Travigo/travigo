package routes

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

func CalculatedRoute(c *fiber.Ctx) error {
	if view := c.Query("view"); view != "" && view != "web" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "unsupported view"})
	}
	collection := database.GetCollection("stats")

	var statsRecords []bson.M
	cursor, _ := collection.Find(context.Background(), bson.M{})
	cursor.All(context.Background(), &statsRecords)

	statsRecordsMap := map[string]bson.M{}
	for _, statsRecord := range statsRecords {
		statsType := statsRecord["type"].(string)
		if c.Query("view") == "web" {
			delete(statsRecord, "_id")
			delete(statsRecord, "type")
		}
		statsRecordsMap[statsType] = statsRecord
	}

	return c.JSON(statsRecordsMap)
}
