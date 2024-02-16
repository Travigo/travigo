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
	router.Post("/notificationtoken", postNotificationToken)
}

func postNotificationToken(c *fiber.Ctx) error {
	var requestBody struct {
		Token string
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
	}

	userPushNotificationTargetCollection := database.GetCollection("user_push_notification_target")

	filter := bson.M{"userid": userID}
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
