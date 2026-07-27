package ctdf

import (
	"time"
)

type Event struct {
	Type      EventType
	Timestamp time.Time
	Body      interface{}
}

type EventType string

const (
	EventTypeServiceAlertCreated EventType = "ServiceAlertCreated"

	EventTypeRealtimeJourneyCreated             EventType = "RealtimeJourneyCreated"
	EventTypeRealtimeJourneyActivelyTracked     EventType = "RealtimeJourneyActivelyTracked"
	EventTypeRealtimeJourneyPlatformSet         EventType = "RealtimeJourneyPlatformSet"
	EventTypeRealtimeJourneyPlatformChanged     EventType = "RealtimeJourneyPlatformChanged"
	EventTypeRealtimeJourneyCancelled           EventType = "RealtimeJourneyCancelled"
	EventTypeRealtimeJourneyOverlayCreated      EventType = "RealtimeJourneyOverlayCreated"
	EventTypeRealtimeJourneyLocationTextChanged EventType = "RealtimeJourneyLocationTextChanged"
	EventTypeRealtimeJourneyNextStopChanged     EventType = "RealtimeJourneyNextStopChanged"
)

func (eventType EventType) Valid() bool {
	switch eventType {
	case EventTypeServiceAlertCreated,
		EventTypeRealtimeJourneyCreated,
		EventTypeRealtimeJourneyActivelyTracked,
		EventTypeRealtimeJourneyPlatformSet,
		EventTypeRealtimeJourneyPlatformChanged,
		EventTypeRealtimeJourneyCancelled,
		EventTypeRealtimeJourneyOverlayCreated,
		EventTypeRealtimeJourneyLocationTextChanged,
		EventTypeRealtimeJourneyNextStopChanged:
		return true
	default:
		return false
	}
}

type EventNotificationData struct {
	Title   string
	Message string
}
