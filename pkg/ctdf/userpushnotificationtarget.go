package ctdf

import "time"

type UserPushNotificationTarget struct {
	UserID                string    `json:"-"`
	ModificationDateTime  time.Time `groups:"web-notification-target"`
	PushNotificationToken string    `groups:"web-notification-target"`

	DeviceType   UserPushNotificationTargetDeviceType `groups:"web-notification-target"`
	DeviceVendor string                               `groups:"web-notification-target"`
	DeviceModel  string                               `groups:"web-notification-target"`
}

type UserPushNotificationTargetDeviceType string

const (
	UserPushNotificationTargetDeviceTypePWA UserPushNotificationTargetDeviceType = "PWA"
)
