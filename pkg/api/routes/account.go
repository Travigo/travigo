package routes

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func AccountRouter(router fiber.Router) {
	router.Get("/notificationtoken", listNotificationTokens)
	router.Post("/notificationtoken", postNotificationToken)
	router.Delete("/notificationtoken", deleteNotificationToken)
	router.Delete("/notificationtoken/:token", deleteNotificationToken)
}

func listNotificationTokens(c *fiber.Ctx) error {
	userID := c.Locals("account_userid").(string)

	var userPushNotificationTargets []*ctdf.UserPushNotificationTarget

	userPushNotificationTargetCollection := database.GetCollection("user_push_notification_target")
	cursor, err := userPushNotificationTargetCollection.Find(context.Background(), bson.M{"userid": userID}, options.Find().SetSort(bson.D{
		{Key: "modificationdatetime", Value: -1},
	}))
	if err != nil {
		c.SendStatus(fiber.StatusInternalServerError)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	for cursor.Next(context.Background()) {
		var userPushNotificationTarget *ctdf.UserPushNotificationTarget
		err := cursor.Decode(&userPushNotificationTarget)
		if err != nil {
			c.SendStatus(fiber.StatusInternalServerError)
			return c.JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		userPushNotificationTargets = append(userPushNotificationTargets, userPushNotificationTarget)
	}
	if err := cursor.Err(); err != nil {
		c.SendStatus(fiber.StatusInternalServerError)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(userPushNotificationTargets)
}

func postNotificationToken(c *fiber.Ctx) error {
	var requestBody struct {
		Token        string
		DeviceType   ctdf.UserPushNotificationTargetDeviceType
		DeviceVendor string
		DeviceModel  string
	}
	c.BodyParser(&requestBody)

	userID := c.Locals("account_userid").(string)

	if userID == "" {
		c.SendStatus(fiber.StatusInternalServerError)
		return c.JSON(fiber.Map{
			"error": "No userid set",
		})
	}

	if requestBody.Token == "" {
		c.SendStatus(fiber.StatusInternalServerError)
		return c.JSON(fiber.Map{
			"error": "No token set",
		})
	}

	userPushNotificationTarget := ctdf.UserPushNotificationTarget{
		UserID:                userID,
		PushNotificationToken: requestBody.Token,
		ModificationDateTime:  time.Now(),
		DeviceType:            requestBody.DeviceType,
		DeviceVendor:          requestBody.DeviceVendor,
		DeviceModel:           requestBody.DeviceModel,
	}

	userPushNotificationTargetCollection := database.GetCollection("user_push_notification_target")

	filter := bson.M{
		"userid":                userID,
		"pushnotificationtoken": requestBody.Token,
	}
	update := bson.M{"$set": userPushNotificationTarget}
	opts := options.Update().SetUpsert(true)
	_, err := userPushNotificationTargetCollection.UpdateOne(context.Background(), filter, update, opts)

	if err == nil {
		return c.JSON(fiber.Map{
			"success": true,
		})
	} else {
		c.SendStatus(fiber.StatusInternalServerError)
		return c.JSON(fiber.Map{
			"error": err,
		})
	}
}

func deleteNotificationToken(c *fiber.Ctx) error {
	var requestBody struct {
		Token string
	}
	c.BodyParser(&requestBody)

	userID := c.Locals("account_userid").(string)

	token := requestBody.Token
	if token == "" {
		token = c.Params("token")
	}

	if token == "" {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": "No token set",
		})
	}

	userPushNotificationTargetCollection := database.GetCollection("user_push_notification_target")
	result, err := userPushNotificationTargetCollection.DeleteOne(context.Background(), bson.M{
		"userid":                userID,
		"pushnotificationtoken": token,
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
			"error": "Notification token not found",
		})
	}

	return c.JSON(fiber.Map{
		"success": true,
	})
}
