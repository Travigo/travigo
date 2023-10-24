package ctdf

import "time"

type Event struct {
	Type      EventType
	Timestamp time.Time
	Body      interface{}
}

type EventType string

const (
	EventTypeServiceAlertCreated EventType = "ServiceAlertCreated"
)

func (e *Event) GetNotificationData() EventNotificationData {
	eventNotificationData := EventNotificationData{}

	eventBody := e.Body.(map[string]interface{})

	switch e.Type {
	case EventTypeServiceAlertCreated:
		eventNotificationData.Title = eventBody["AlertType"].(string)
		eventNotificationData.Message = eventBody["Text"].(string)

		title := eventBody["Title"].(string)
		if title != "" {
			eventNotificationData.Title = title
		}
	}

	return eventNotificationData
}

type EventNotificationData struct {
	Title   string
	Message string
}
