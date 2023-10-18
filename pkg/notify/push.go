package notify

import (
	"context"
	"encoding/base64"
	"errors"
	"os"

	firebase "firebase.google.com/go"
	"firebase.google.com/go/messaging"
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
	var userPushNotificationTarget *ctdf.UserPushNotificationTarget

	userPushNotificationTargetCollection.FindOne(context.Background(), bson.M{
		"userid": notification.TargetUser,
	}).Decode(&userPushNotificationTarget)

	if userPushNotificationTarget == nil {
		return errors.New("failed to find user token")
	}

	fcmClient, err := m.FirebaseApp.Messaging(context.Background())

	if err != nil {
		return err
	}

	_, err = fcmClient.Send(context.Background(), &messaging.Message{
		Notification: &messaging.Notification{
			Title: notification.Title,
			Body:  notification.Message,
		},
		Token: userPushNotificationTarget.PushNotificationToken,
	})

	if err != nil {
		return err
	}

	log.Info().Str("target", notification.TargetUser).Msg("Sent Push Notification")

	return nil
}
