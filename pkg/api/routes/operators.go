package routes

import (
	"context"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/transforms"
	"go.mongodb.org/mongo-driver/bson"
)

type operatorRegions struct {
	Name      string
	Operators []interface{}
}

func OperatorsRouter(router fiber.Router) {
	router.Get("/", listOperators)
	router.Get("/:identifier", getOperator)
	router.Get("/:identifier/services", getOperatorServices)
}

func listOperators(c *fiber.Ctx) error {
	regions := map[string]string{
		"UK:REGION:LONDON":       "London",
		"UK:REGION:SOUTHWEST":    "South West England",
		"UK:REGION:WESTMIDLANDS": "West Midlands",
		"UK:REGION:WALES":        "Wales",
		"UK:REGION:YORKSHIRE":    "Yorkshire",
		"UK:REGION:NORTHWEST":    "North West England",
		"UK:REGION:NORTHEAST":    "North East England",
		"UK:REGION:SCOTLAND":     "Scotland",
		"UK:REGION:SOUTHEAST":    "South East England",
		"UK:REGION:EASTANGLIA":   "East Anglia",
		"UK:REGION:EASTMIDLANDS": "East Midlands",
		// "UK:REGION:NORTHERNIRELAND": "Northern Ireland",
	}
	regionOperators := map[string]*operatorRegions{} //[]*ctdf.Operator{}
	// The below query can return the same operator many times
	// this map allows us to efficiently check if we've already added operator to a region
	operatorInRegionCheck := map[string]map[string]bool{} // REGION OPERATOR BOOL
	for key, name := range regions {
		regionOperators[key] = &operatorRegions{
			Name:      name,
			Operators: []interface{}{},
		}
		operatorInRegionCheck[key] = map[string]bool{}
	}

	operatorsCollection := database.GetCollection("operators")
	servicesCollection := database.GetCollection("services")

	operatorNames, _ := servicesCollection.Distinct(context.Background(), "operatorref", bson.D{})
	operatorNamesArray := bson.A{}
	for _, name := range operatorNames {
		operatorNamesArray = append(operatorNamesArray, name)
	}

	operators, _ := operatorsCollection.Find(context.Background(), bson.M{"otheridentifiers": bson.M{"$in": operatorNamesArray}})

	for operators.Next(context.TODO()) {
		var operator *ctdf.Operator
		operators.Decode(&operator)

		for _, region := range operator.Regions {

			if !operatorInRegionCheck[region][operator.PrimaryIdentifier] {
				reducedOperator, _ := sheriff.Marshal(&sheriff.Options{
					Groups: []string{"basic"},
				}, operator)

				regionOperators[region].Operators = append(regionOperators[region].Operators, reducedOperator)
				operatorInRegionCheck[region][operator.PrimaryIdentifier] = true
			}
		}
	}

	// Alphabetically sort operators by name
	// Code is a bit vom
	for _, operatorRegion := range regionOperators {
		sort.SliceStable(operatorRegion.Operators, func(i, j int) bool {
			return strings.Compare(
				operatorRegion.Operators[i].(map[string]interface{})["PrimaryName"].(string),
				operatorRegion.Operators[j].(map[string]interface{})["PrimaryName"].(string)) < 0
		})
	}

	return c.JSON(regionOperators)
}

func getOperator(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	operator, err := getOperatorById(identifier)

	if err != nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	} else {
		operator.GetReferences()
		return c.JSON(operator)
	}
}

func getOperatorServices(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	operator, err := getOperatorById(identifier)

	if err != nil {
		c.SendStatus(404)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	} else {
		var services []*ctdf.Service

		servicesCollection := database.GetCollection("services")
		cursor, _ := servicesCollection.Find(context.Background(), bson.M{"operatorref": bson.M{"$in": operator.OtherIdentifiers}})

		for cursor.Next(context.TODO()) {
			var service ctdf.Service
			err := cursor.Decode(&service)
			if err != nil {
				log.Error().Err(err).Msg("Failed to decode Service")
			}

			services = append(services, &service)
		}

		transforms.Transform(services, 3)

		return c.JSON(services)
	}
}

func getOperatorById(identifier string) (*ctdf.Operator, error) {
	var operator *ctdf.Operator
	operator, err := dataaggregator.Lookup[*ctdf.Operator](query.Operator{
		AnyIdentifier: identifier,
	})

	transforms.Transform(operator, 2)

	return operator, err
}
