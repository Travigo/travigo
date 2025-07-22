package ctdf

type Notification struct {
	TargetUser string
	Type       NotificationType

	Title   string
	Message string
}

type NotificationType string

const (
	NotificationTypePush  NotificationType = "Push"
	NotificationTypeEmail NotificationType = "Email"
)
