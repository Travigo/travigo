package routes

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const defaultUserNotificationSubscriptionLimit int64 = 10

type notificationSubscriptionRequest struct {
	EventType ctdf.EventType                          `json:"eventType"`
	Values    ctdf.UserNotificationSubscriptionValues `json:"values"`
}

type notificationSubscriptionQuota struct {
	Used      int64 `json:"used"`
	Limit     int64 `json:"limit"`
	Remaining int64 `json:"remaining"`
}

func NotificationSubscriptionsRouter(router fiber.Router) {
	router.Get("/", listNotificationSubscriptions)
	router.Get("/quota", getNotificationSubscriptionQuota)
	router.Post("/", createNotificationSubscription)
	router.Put("/:identifier", updateNotificationSubscription)
	router.Delete("/:identifier", deleteNotificationSubscription)
}

// NotificationSubscriptionLimitForUser is the single place where a user's
// subscription allowance is determined. It can later incorporate account plans
// or entitlements without changing the API handlers.
func NotificationSubscriptionLimitForUser(_ string) int64 {
	return defaultUserNotificationSubscriptionLimit
}

func listNotificationSubscriptions(c *fiber.Ctx) error {
	userID := c.Locals("account_userid").(string)
	subscriptions, err := getUserNotificationSubscriptions(userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	if subscriptions == nil {
		subscriptions = []ctdf.UserNotificationSubscription{}
	}

	return c.JSON(subscriptions)
}

func getNotificationSubscriptionQuota(c *fiber.Ctx) error {
	userID := c.Locals("account_userid").(string)
	used, err := getUserNotificationSubscriptionCount(userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(buildNotificationSubscriptionQuota(userID, used))
}

func createNotificationSubscription(c *fiber.Ctx) error {
	var request notificationSubscriptionRequest
	if err := c.BodyParser(&request); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid notification subscription"})
	}
	if validationError := validateNotificationSubscriptionRequest(request); validationError != "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": validationError})
	}

	userID := c.Locals("account_userid").(string)
	now := time.Now()
	subscription := ctdf.UserNotificationSubscription{
		PrimaryIdentifier:    "travigo-notification-subscription-" + primitive.NewObjectID().Hex(),
		UserID:               userID,
		EventType:            request.EventType,
		Values:               request.Values,
		CreationDateTime:     now,
		ModificationDateTime: now,
	}

	collection := database.GetCollection("user_notification_subscriptions")
	limit := NotificationSubscriptionLimitForUser(userID)
	used, err := getUserNotificationSubscriptionCount(userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if used >= limit {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "Notification subscription limit reached",
			"quota": buildNotificationSubscriptionQuota(userID, used),
		})
	}

	if _, err := collection.InsertOne(context.Background(), subscription); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.Status(fiber.StatusCreated).JSON(subscription)
}

func updateNotificationSubscription(c *fiber.Ctx) error {
	var request notificationSubscriptionRequest
	if err := c.BodyParser(&request); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid notification subscription"})
	}
	if validationError := validateNotificationSubscriptionRequest(request); validationError != "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": validationError})
	}

	userID := c.Locals("account_userid").(string)
	identifier := c.Params("identifier")
	now := time.Now()

	collection := database.GetCollection("user_notification_subscriptions")
	var subscription ctdf.UserNotificationSubscription
	err := collection.FindOneAndUpdate(
		context.Background(),
		bson.M{
			"userid":            userID,
			"primaryidentifier": identifier,
		},
		bson.M{"$set": bson.M{
			"eventtype":            request.EventType,
			"values":               request.Values,
			"modificationdatetime": now,
		}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&subscription)
	if err == mongo.ErrNoDocuments {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Notification subscription not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(subscription)
}

func deleteNotificationSubscription(c *fiber.Ctx) error {
	userID := c.Locals("account_userid").(string)
	identifier := c.Params("identifier")

	collection := database.GetCollection("user_notification_subscriptions")
	result, err := collection.DeleteOne(
		context.Background(),
		bson.M{
			"userid":            userID,
			"primaryidentifier": identifier,
		},
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if result.DeletedCount == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Notification subscription not found"})
	}

	return c.JSON(fiber.Map{"success": true})
}

func getUserNotificationSubscriptions(userID string) ([]ctdf.UserNotificationSubscription, error) {
	cursor, err := database.GetCollection("user_notification_subscriptions").Find(
		context.Background(),
		bson.M{"userid": userID},
		options.Find().SetSort(bson.D{{Key: "creationdatetime", Value: -1}}),
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())

	subscriptions := []ctdf.UserNotificationSubscription{}
	if err := cursor.All(context.Background(), &subscriptions); err != nil {
		return nil, err
	}

	return subscriptions, nil
}

func getUserNotificationSubscriptionCount(userID string) (int64, error) {
	return database.GetCollection("user_notification_subscriptions").
		CountDocuments(context.Background(), bson.M{"userid": userID})
}

func buildNotificationSubscriptionQuota(userID string, used int64) notificationSubscriptionQuota {
	limit := NotificationSubscriptionLimitForUser(userID)
	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}

	return notificationSubscriptionQuota{
		Used:      used,
		Limit:     limit,
		Remaining: remaining,
	}
}

func validateNotificationSubscriptionRequest(request notificationSubscriptionRequest) string {
	if !request.EventType.Valid() {
		return "Invalid notification event type"
	}
	if len(request.Values.ServiceAlertTypes) == 0 &&
		request.Values.StopRef == "" &&
		request.Values.ServiceRef == "" {
		return "No notification values set"
	}

	return ""
}
