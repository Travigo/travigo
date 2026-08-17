package routes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type commuteRequest struct {
	Name                     string   `json:"name"`
	OriginRef                string   `json:"originRef"`
	DestinationRef           string   `json:"destinationRef"`
	DaysOfWeek               []string `json:"daysOfWeek"`
	ArrivalAtDestinationTime string   `json:"arrivalAtDestinationTime"`
	ReturnDepartureTime      string   `json:"returnDepartureTime"`
}

func CommutesRouter(router fiber.Router) {
	router.Get("/", listCommutes)
	router.Post("/", createCommute)
	router.Put("/:identifier", updateCommute)
	router.Delete("/:identifier", deleteCommute)
}

func listCommutes(c *fiber.Ctx) error {
	commutes, err := getUserCommutes(c.Locals("account_userid").(string))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if commutes == nil {
		commutes = []ctdf.UserCommute{}
	}
	for index := range commutes {
		populateCommute(&commutes[index])
	}
	return c.JSON(commutes)
}

func createCommute(c *fiber.Ctx) error {
	request, response := parseAndValidateCommuteRequest(c)
	if response != nil {
		return response
	}
	userID := c.Locals("account_userid").(string)
	now := time.Now()
	commute := ctdf.UserCommute{
		PrimaryIdentifier:        "travigo-commute-" + primitive.NewObjectID().Hex(),
		UserID:                   userID,
		Name:                     request.Name,
		OriginRef:                request.OriginRef,
		DestinationRef:           request.DestinationRef,
		DaysOfWeek:               request.DaysOfWeek,
		ArrivalAtDestinationTime: request.ArrivalAtDestinationTime,
		ReturnDepartureTime:      request.ReturnDepartureTime,
		CreationDateTime:         now,
		ModificationDateTime:     now,
	}
	if _, err := database.GetCollection("user_commutes").InsertOne(context.Background(), commute); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	populateCommute(&commute)
	return c.Status(fiber.StatusCreated).JSON(commute)
}

func updateCommute(c *fiber.Ctx) error {
	request, response := parseAndValidateCommuteRequest(c)
	if response != nil {
		return response
	}
	commute := ctdf.UserCommute{}
	err := database.GetCollection("user_commutes").FindOneAndUpdate(
		context.Background(),
		bson.M{"userid": c.Locals("account_userid").(string), "primaryidentifier": c.Params("identifier")},
		bson.M{"$set": bson.M{
			"name": request.Name, "originref": request.OriginRef, "destinationref": request.DestinationRef,
			"daysofweek": request.DaysOfWeek, "arrivalatdestinationtime": request.ArrivalAtDestinationTime,
			"returndeparturetime": request.ReturnDepartureTime, "modificationdatetime": time.Now(),
		}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&commute)
	if err == mongo.ErrNoDocuments {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Commute not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	populateCommute(&commute)
	return c.JSON(commute)
}

func deleteCommute(c *fiber.Ctx) error {
	result, err := database.GetCollection("user_commutes").DeleteOne(context.Background(), bson.M{
		"userid": c.Locals("account_userid").(string), "primaryidentifier": c.Params("identifier"),
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if result.DeletedCount == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Commute not found"})
	}
	return c.JSON(fiber.Map{"success": true})
}

func parseAndValidateCommuteRequest(c *fiber.Ctx) (commuteRequest, error) {
	request := commuteRequest{}
	if err := c.BodyParser(&request); err != nil {
		return commuteRequest{}, c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid commute"})
	}
	if message := validateCommuteRequest(request); message != "" {
		return commuteRequest{}, c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": message})
	}
	for _, stopRef := range []string{request.OriginRef, request.DestinationRef} {
		if _, err := dataaggregator.Lookup[*ctdf.Stop](query.Stop{Identifier: stopRef}); err != nil {
			return commuteRequest{}, c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("Unknown commute stop %q", stopRef)})
		}
	}
	return request, nil
}

func validateCommuteRequest(request commuteRequest) string {
	if strings.TrimSpace(request.Name) == "" {
		return "Commute name is required"
	}
	if request.OriginRef == "" || request.DestinationRef == "" || request.OriginRef == request.DestinationRef {
		return "Choose two different commute stops"
	}
	if !validCommuteClockTime(request.ArrivalAtDestinationTime) || !validCommuteClockTime(request.ReturnDepartureTime) {
		return "Commute times must be in HH:MM format"
	}
	if len(request.DaysOfWeek) == 0 {
		return "Choose at least one commute day"
	}
	for _, day := range request.DaysOfWeek {
		if !ctdf.ValidNotificationDay(day) {
			return "Invalid commute day"
		}
	}
	return ""
}

func validCommuteClockTime(value string) bool {
	parsed, err := time.Parse("15:04", value)
	return err == nil && parsed.Format("15:04") == value
}

func getUserCommutes(userID string) ([]ctdf.UserCommute, error) {
	cursor, err := database.GetCollection("user_commutes").Find(context.Background(), bson.M{"userid": userID}, options.Find().SetSort(bson.D{{Key: "creationdatetime", Value: -1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())
	commutes := []ctdf.UserCommute{}
	if err := cursor.All(context.Background(), &commutes); err != nil {
		return nil, err
	}
	return commutes, nil
}

func populateCommute(commute *ctdf.UserCommute) {
	if commute == nil {
		return
	}
	origin, _ := dataaggregator.Lookup[*ctdf.Stop](query.Stop{Identifier: commute.OriginRef})
	destination, _ := dataaggregator.Lookup[*ctdf.Stop](query.Stop{Identifier: commute.DestinationRef})
	commute.Origin = ctdf.NewCommuteStop(origin)
	commute.Destination = ctdf.NewCommuteStop(destination)
}
