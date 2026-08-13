package ctdf

type Notification struct {
	TargetUser string
	Type       NotificationType

	Title   string
	Message string
	URL     string `json:"url,omitempty"`
}

type NotificationType string

const (
	NotificationTypePush  NotificationType = "Push"
	NotificationTypeEmail NotificationType = "Email"
)
