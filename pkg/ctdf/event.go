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

	EventTypeRealtimeJourneyCreated         = "RealtimeJourneyCreated"
	EventTypeRealtimeJourneyPlatformSet     = "RealtimeJourneyPlatformSet"
	EventTypeRealtimeJourneyPlatformChanged = "RealtimeJourneyPlatformChanged"
	EventTypeRealtimeJourneyCancelled       = "RealtimeJourneyCancelled"
)

type EventNotificationData struct {
	Title   string
	Message string
}
