package notify

import (
	"context"
	"encoding/base64"
	"errors"
	"os"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/api/option"
)

type PushManager struct {
	FirebaseApp *firebase.App
}

func (m *PushManager) Setup() error {
	fireBaseAuthKey := os.Getenv("TRAVIGO_FIREBASE_SERVICE_ACCOUNT")

	decodedKey, err := base64.StdEncoding.DecodeString(fireBaseAuthKey)
	if err != nil {
		return err
	}

	opts := []option.ClientOption{option.WithCredentialsJSON(decodedKey)}

	// Initialize firebase app
	app, err := firebase.NewApp(context.Background(), nil, opts...)

	if err != nil {
		return err
	}

	m.FirebaseApp = app

	return nil
}

func (m *PushManager) SendPush(notification ctdf.Notification) error {
	userPushNotificationTargetCollection := database.GetCollection("user_push_notification_target")

	cursor, err := userPushNotificationTargetCollection.Find(context.Background(), bson.M{
		"userid": notification.TargetUser,
	})
	if err != nil {
		return err
	}

	var userPushNotificationTargets []*ctdf.UserPushNotificationTarget
	for cursor.Next(context.Background()) {
		var userPushNotificationTarget *ctdf.UserPushNotificationTarget
		err := cursor.Decode(&userPushNotificationTarget)
		if err != nil {
			log.Error().Err(err).Str("target", notification.TargetUser).Msg("Failed to decode push notification target")
			continue
		}

		userPushNotificationTargets = append(userPushNotificationTargets, userPushNotificationTarget)
	}
	if err := cursor.Err(); err != nil {
		return err
	}

	if len(userPushNotificationTargets) == 0 {
		return errors.New("failed to find user token")
	}

	fcmClient, err := m.FirebaseApp.Messaging(context.Background())

	if err != nil {
		return err
	}

	sentCount := 0
	var lastErr error
	for _, userPushNotificationTarget := range userPushNotificationTargets {
		_, err = fcmClient.Send(context.Background(), &messaging.Message{
			Notification: &messaging.Notification{
				Title: notification.Title,
				Body:  notification.Message,
			},
			Token: userPushNotificationTarget.PushNotificationToken,
		})

		if err != nil {
			lastErr = err
			log.Error().Err(err).Str("target", notification.TargetUser).Msg("Failed to send push notification")
			continue
		}

		sentCount++
	}

	if sentCount == 0 {
		return lastErr
	}

	log.Info().Str("target", notification.TargetUser).Int("tokens", sentCount).Msg("Sent Push Notification")

	return nil
}
