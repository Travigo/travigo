package routes

import (
	"context"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/stats"
	"go.mongodb.org/mongo-driver/bson"
)

func IdentificationRateRouter(router fiber.Router) {
	router.Get("/", getIdentificationRate)
}

func getIdentificationRate(c *fiber.Ctx) error {
	operatorsListString := c.Query("operators", "")

	var operatorsList []string
	if operatorsListString != "" {
		operatorsList = strings.Split(operatorsListString, ",")

		// Get the other identifiers for each of the operators as we have many different forms (eg. NOCID or NOC)
		for _, operatorID := range operatorsList {
			operatorsCollection := database.GetCollection("operators")
			var operator *ctdf.Operator
			operatorsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": operatorID}).Decode(&operator)

			if operator != nil {
				operatorsList = append(operatorsList, operator.OtherIdentifiers...)
			}
		}
	}

	rateStats := stats.GetIdentificationRateStats(operatorsList)

	return c.JSON(rateStats)
}
