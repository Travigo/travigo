package ctdf

import "time"

type UserPushNotificationTarget struct {
	UserID                string
	ModificationDateTime  time.Time
	PushNotificationToken string

	DeviceType   UserPushNotificationTargetDeviceType
	DeviceVendor string
	DeviceModel  string
}

type UserPushNotificationTargetDeviceType string

const (
	UserPushNotificationTargetDeviceTypePWA UserPushNotificationTargetDeviceType = "PWA"
)
