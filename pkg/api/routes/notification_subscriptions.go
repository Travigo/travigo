package routes

import (
	"context"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/transforms"
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

	return writeNotificationSubscriptions(c, subscriptions)
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
	if view := c.Query("view"); view != "" && view != "web" {
		return sheriffViewError(c, fmt.Errorf("unsupported view %q", view))
	}

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

	if c.Query("view") == "web" {
		populateNotificationSubscription(&subscription)
		reduced, marshalErr := sheriff.Marshal(&sheriff.Options{Groups: []string{"web-notification-subscription", "web-notification"}}, subscription)
		if marshalErr != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": marshalErr.Error()})
		}
		return c.Status(fiber.StatusCreated).JSON(reduced)
	}
	if c.Query("view") != "" {
		return sheriffViewError(c, fmt.Errorf("unsupported view %q", c.Query("view")))
	}
	return c.Status(fiber.StatusCreated).JSON(subscription)
}

func updateNotificationSubscription(c *fiber.Ctx) error {
	if view := c.Query("view"); view != "" && view != "web" {
		return sheriffViewError(c, fmt.Errorf("unsupported view %q", view))
	}

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

	if c.Query("view") == "web" {
		populateNotificationSubscription(&subscription)
		reduced, marshalErr := sheriff.Marshal(&sheriff.Options{Groups: []string{"web-notification-subscription", "web-notification"}}, subscription)
		if marshalErr != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": marshalErr.Error()})
		}
		return c.JSON(reduced)
	}
	if c.Query("view") != "" {
		return sheriffViewError(c, fmt.Errorf("unsupported view %q", c.Query("view")))
	}
	return c.JSON(subscription)
}

func writeNotificationSubscriptions(c *fiber.Ctx, subscriptions []ctdf.UserNotificationSubscription) error {
	if c.Query("view") == "" {
		return c.JSON(subscriptions)
	}
	if c.Query("view") != "web" {
		return sheriffViewError(c, fmt.Errorf("unsupported view %q", c.Query("view")))
	}
	for index := range subscriptions {
		populateNotificationSubscription(&subscriptions[index])
	}
	reduced, err := sheriff.Marshal(&sheriff.Options{Groups: []string{"web-notification-subscription", "web-notification"}}, subscriptions)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(reduced)
}

func populateNotificationSubscription(subscription *ctdf.UserNotificationSubscription) {
	if subscription == nil {
		return
	}
	if subscription.Values.StopRef != "" {
		subscription.Subject, _ = dataaggregator.Lookup[*ctdf.Stop](query.Stop{Identifier: subscription.Values.StopRef})
	} else if subscription.Values.ServiceRef != "" {
		service, _ := dataaggregator.Lookup[*ctdf.Service](query.Service{PrimaryIdentifier: subscription.Values.ServiceRef})
		transforms.Transform(service, 1)
		subscription.Subject = service
	} else if subscription.Values.JourneyRef != "" {
		journey, _ := dataaggregator.Lookup[*ctdf.Journey](query.Journey{PrimaryIdentifier: subscription.Values.JourneyRef})
		if journey != nil {
			journey.GetReferences()
			transforms.Transform(journey.Service, 1)
			if len(journey.Path) > 0 {
				journey.Path[0].GetOriginStop()
				if journey.Path[0].OriginStop != nil {
					journey.OriginDisplay = journey.Path[0].OriginStop.PrimaryName
				}
			}
		}
		subscription.Subject = journey
	}
	for _, stopRef := range subscription.Values.StopRefs {
		stop, err := dataaggregator.Lookup[*ctdf.Stop](query.Stop{Identifier: stopRef})
		if err == nil && stop != nil {
			subscription.PlatformStops = append(subscription.PlatformStops, stop)
		}
	}
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
		request.Values.ServiceRef == "" &&
		request.Values.JourneyRef == "" &&
		len(request.Values.StopRefs) == 0 {
		return "No notification values set"
	}

	return ""
}
