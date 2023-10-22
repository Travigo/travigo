package ctdf

type UserEventSubscription struct {
	UserID string

	EventType        EventType
	NotificationType NotificationType

	Expression string
}
