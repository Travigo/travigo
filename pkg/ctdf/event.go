package ctdf

import (
	"fmt"
	"time"

	"github.com/kr/pretty"
)

type Event struct {
	Type      EventType
	Timestamp time.Time
	Body      interface{}
}

type EventType string

const (
	EventTypeServiceAlertCreated EventType = "ServiceAlertCreated"

	EventTypeRealtimeJourneyCreated         = "RealtimeJourneyCreated"
	EventTypeRealtimeJourneyPlatformSet     = "RealtimeJourneyPlatformSet"
	EventTypeRealtimeJourneyPlatformChanged = "RealtimeJourneyPlatformChanged"
	EventTypeRealtimeJourneyCancelled       = "RealtimeJourneyCancelled"
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
	case EventTypeRealtimeJourneyCancelled:
		eventNotificationData.Title = "Journey cancelled"

		journey := eventBody["Journey"].(map[string]interface{})

		departureTime, _ := time.Parse(time.RFC3339, journey["DepartureTime"].(string))
		departureTimeText := departureTime.Format("15:04")
		destination := journey["DestinationDisplay"]
		eventNotificationData.Message = fmt.Sprintf("The %s to %s has been cancelled.", departureTimeText, destination)

		if eventBody["Annotations"].(map[string]interface{})["CancelledReasonText"] != nil {
			eventNotificationData.Message = fmt.Sprintf("%s %s", eventNotificationData.Message, eventBody["Annotations"].(map[string]interface{})["CancelledReasonText"])
		}
	case EventTypeRealtimeJourneyPlatformSet, EventTypeRealtimeJourneyPlatformChanged:
		eventNotificationData.Title = "Platform Update"

		realtimeJourney := eventBody["RealtimeJourney"].(map[string]interface{})
		journey := realtimeJourney["Journey"].(map[string]interface{})
		originStopName := eventBody["Stop"].(string)
		realtimeJourneyStop := realtimeJourney["Stops"].(map[string]interface{})[originStopName].(map[string]interface{})

		departureTime := "N/A"
		destination := journey["DestinationDisplay"]
		originStop := originStopName
		platform := realtimeJourneyStop["Platform"]

		if e.Type == EventTypeRealtimeJourneyPlatformSet {
			eventNotificationData.Message = fmt.Sprintf("The %s service to %s from %s will depart from platform %s", departureTime, destination, originStop, platform)
		} else if e.Type == EventTypeRealtimeJourneyPlatformChanged {
			oldPlatform := eventBody["OldPlatform"]
			eventNotificationData.Message = fmt.Sprintf("The %s service to %s from %s will now be departing from platform %s instead of %s", departureTime, destination, originStop, platform, oldPlatform)
		}
	}

	pretty.Println(eventNotificationData)

	return eventNotificationData
}

type EventNotificationData struct {
	Title   string
	Message string
}
