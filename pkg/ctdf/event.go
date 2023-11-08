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

	EventTypeRealtimeJourneyCreated             = "RealtimeJourneyCreated"
	EventTypeRealtimeJourneyActivelyTracked     = "RealtimeJourneyActivelyTracked"
	EventTypeRealtimeJourneyPlatformSet         = "RealtimeJourneyPlatformSet"
	EventTypeRealtimeJourneyPlatformChanged     = "RealtimeJourneyPlatformChanged"
	EventTypeRealtimeJourneyCancelled           = "RealtimeJourneyCancelled"
	EventTypeRealtimeJourneyLocationTextChanged = "RealtimeJourneyLocationTextChanged"
	EventTypeRealtimeJourneyNextStopChanged     = "RealtimeJourneyNextStopChanged"
)

type EventNotificationData struct {
	Title   string
	Message string
}
