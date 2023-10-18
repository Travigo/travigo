package ctdf

type UserEventNotificationExpression struct {
	UserID string

	EventType        EventType
	NotificationType NotificationType

	Expression string
}
