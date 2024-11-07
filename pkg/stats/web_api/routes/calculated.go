package routes

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

func CalculatedRoute(c *fiber.Ctx) error {
	collection := database.GetCollection("stats")

	var statsRecords []bson.M
	cursor, _ := collection.Find(context.Background(), bson.M{})
	cursor.All(context.Background(), &statsRecords)

	statsRecordsMap := map[string]bson.M{}
	for _, statsRecord := range statsRecords {
		statsRecordsMap[statsRecord["type"].(string)] = statsRecord
	}

	return c.JSON(statsRecordsMap)
}
