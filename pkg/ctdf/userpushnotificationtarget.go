package ctdf

import "time"

type UserPushNotificationTarget struct {
	UserID                string
	ModificationDateTime  time.Time
	PushNotificationToken string
}
