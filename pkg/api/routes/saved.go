package routes

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/transforms"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const maxSavedObjectsPerUser = 20

func SavedRouter(router fiber.Router) {
	router.Get("/", listSavedObjects)
	router.Get("/:identifier", getSavedObject)
	router.Delete("/:identifier", deleteSavedObject)
	router.Post("/", createSavedObject)
}

func listSavedObjects(c *fiber.Ctx) error {
	userID := c.Locals("account_userid").(string)

	var savedObjects []*ctdf.SavedObject

	collection := database.GetCollection("saved_objects")
	cursor, err := collection.Find(context.Background(), bson.M{"userid": userID}, options.Find().SetSort(bson.D{
		{Key: "type", Value: 1},
		{Key: "objectidentifier", Value: 1},
	}))
	if err != nil {
		c.SendStatus(fiber.StatusInternalServerError)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	for cursor.Next(context.Background()) {
		var savedObject *ctdf.SavedObject
		err := cursor.Decode(&savedObject)
		if err != nil {
			c.SendStatus(fiber.StatusInternalServerError)
			return c.JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		savedObjects = append(savedObjects, savedObject)
	}

	if c.Query("view") == "web" {
		for _, savedObject := range savedObjects {
			populateSavedObject(savedObject)
		}
		reduced, marshalErr := sheriff.Marshal(&sheriff.Options{Groups: []string{"web-saved"}}, savedObjects)
		if marshalErr != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": marshalErr.Error()})
		}
		return c.JSON(reduced)
	}
	if c.Query("view") != "" {
		return sheriffViewError(c, fmt.Errorf("unsupported view %q", c.Query("view")))
	}
	return c.JSON(savedObjects)
}

func populateSavedObject(savedObject *ctdf.SavedObject) {
	if savedObject == nil || savedObject.ObjectIdentifier == "" {
		return
	}
	switch strings.ToLower(savedObject.Type) {
	case "stop", "stops":
		stop, err := dataaggregator.Lookup[*ctdf.Stop](query.Stop{Identifier: savedObject.ObjectIdentifier})
		if err != nil {
			return
		}
		stop.Services, _ = dataaggregator.Lookup[[]*ctdf.Service](query.ServicesByStop{Stop: stop})
		transforms.Transform(stop.Services, 1)
		savedObject.Object = stop
	case "journey", "journeys":
		journey, err := dataaggregator.Lookup[*ctdf.Journey](query.Journey{PrimaryIdentifier: savedObject.ObjectIdentifier})
		if err != nil {
			return
		}
		journey.GetService()
		transforms.Transform(journey.Service, 1)
		if len(journey.Path) > 0 {
			journey.Path[0].GetOriginStop()
			if journey.Path[0].OriginStop != nil {
				journey.OriginDisplay = journey.Path[0].OriginStop.PrimaryName
			}
		}
		savedObject.Object = journey
	}
}

func getSavedObject(c *fiber.Ctx) error {
	userID := c.Locals("account_userid").(string)
	identifier := c.Params("identifier")

	var savedObject *ctdf.SavedObject

	collection := database.GetCollection("saved_objects")
	err := collection.FindOne(context.Background(), bson.M{
		"primaryidentifier": identifier,
		"userid":            userID,
	}).Decode(&savedObject)

	if err != nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	if c.Query("view") == "web" {
		populateSavedObject(savedObject)
		return writeSavedObject(c, savedObject)
	}
	if c.Query("view") != "" {
		return sheriffViewError(c, fmt.Errorf("unsupported view %q", c.Query("view")))
	}
	return c.JSON(savedObject)
}

func deleteSavedObject(c *fiber.Ctx) error {
	userID := c.Locals("account_userid").(string)
	identifier := c.Params("identifier")

	collection := database.GetCollection("saved_objects")
	result, err := collection.DeleteOne(context.Background(), bson.M{
		"primaryidentifier": identifier,
		"userid":            userID,
	})

	if err != nil {
		c.SendStatus(fiber.StatusInternalServerError)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	if result.DeletedCount == 0 {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": "Saved object not found",
		})
	}

	return c.JSON(fiber.Map{
		"success": true,
	})
}

func createSavedObject(c *fiber.Ctx) error {
	if view := c.Query("view"); view != "" && view != "web" {
		return sheriffViewError(c, fmt.Errorf("unsupported view %q", view))
	}

	var requestBody struct {
		Type             string
		ObjectIdentifier string
	}
	c.BodyParser(&requestBody)

	userID := c.Locals("account_userid").(string)

	if requestBody.Type == "" {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": "No type set",
		})
	}

	if requestBody.ObjectIdentifier == "" {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": "No object identifier set",
		})
	}

	collection := database.GetCollection("saved_objects")

	savedObject := ctdf.SavedObject{
		PrimaryIdentifier: buildSavedObjectPrimaryIdentifier(userID, requestBody.Type, requestBody.ObjectIdentifier),
		UserID:            userID,
		Type:              requestBody.Type,
		ObjectIdentifier:  requestBody.ObjectIdentifier,
	}

	var existingSavedObject *ctdf.SavedObject
	err := collection.FindOne(context.Background(), bson.M{
		"primaryidentifier": savedObject.PrimaryIdentifier,
		"userid":            userID,
	}).Decode(&existingSavedObject)
	if err == nil {
		return writeSavedObject(c, existingSavedObject)
	}
	if err != mongo.ErrNoDocuments {
		c.SendStatus(fiber.StatusInternalServerError)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	count, err := collection.CountDocuments(context.Background(), bson.M{"userid": userID})
	if err != nil {
		c.SendStatus(fiber.StatusInternalServerError)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	if count >= maxSavedObjectsPerUser {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": "Saved object limit reached",
		})
	}

	_, err = collection.InsertOne(context.Background(), savedObject)
	if err != nil {
		c.SendStatus(fiber.StatusInternalServerError)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return writeSavedObject(c, &savedObject)
}

func writeSavedObject(c *fiber.Ctx, savedObject *ctdf.SavedObject) error {
	if c.Query("view") == "web" {
		reduced, err := marshalWithSheriffView(c, savedObject, nil, map[string][]string{
			"web": {"web-saved"},
		})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(reduced)
	}
	if c.Query("view") != "" {
		return sheriffViewError(c, fmt.Errorf("unsupported view %q", c.Query("view")))
	}
	return c.JSON(savedObject)
}

func buildSavedObjectPrimaryIdentifier(userID string, savedObjectType string, objectIdentifier string) string {
	hash := sha256.New()

	hash.Write([]byte(userID))
	hash.Write([]byte(savedObjectType))
	hash.Write([]byte(objectIdentifier))

	return fmt.Sprintf("travigo-saved-%x", hash.Sum(nil))
}
