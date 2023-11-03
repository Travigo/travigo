package events

import (
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
)

func GetNotificationData(e *ctdf.Event) ctdf.EventNotificationData {
	eventNotificationData := ctdf.EventNotificationData{}

	eventBody := e.Body.(map[string]interface{})

	switch e.Type {
	case ctdf.EventTypeServiceAlertCreated:
		eventNotificationData.Title = eventBody["AlertType"].(string)
		eventNotificationData.Message = eventBody["Text"].(string)

		title := eventBody["Title"].(string)
		if title != "" {
			eventNotificationData.Title = title
		}
	case ctdf.EventTypeRealtimeJourneyCancelled:
		eventNotificationData.Title = "Journey cancelled"

		journey := eventBody["Journey"].(map[string]interface{})

		departureTime, _ := time.Parse(time.RFC3339, journey["DepartureTime"].(string))
		departureTimeText := departureTime.Format("15:04")
		destination := journey["DestinationDisplay"]
		eventNotificationData.Message = fmt.Sprintf("The %s to %s has been cancelled.", departureTimeText, destination)

		if eventBody["Annotations"].(map[string]interface{})["CancelledReasonText"] != nil {
			eventNotificationData.Message = fmt.Sprintf("%s %s", eventNotificationData.Message, eventBody["Annotations"].(map[string]interface{})["CancelledReasonText"])
		}
	case ctdf.EventTypeRealtimeJourneyPlatformSet, ctdf.EventTypeRealtimeJourneyPlatformChanged:
		eventNotificationData.Title = "Platform Update"

		realtimeJourney := eventBody["RealtimeJourney"].(map[string]interface{})
		journey := realtimeJourney["Journey"].(map[string]interface{})
		originStopID := eventBody["Stop"].(string)
		realtimeJourneyStop := realtimeJourney["Stops"].(map[string]interface{})[originStopID].(map[string]interface{})

		var stop *ctdf.Stop
		stop, _ = dataaggregator.Lookup[*ctdf.Stop](query.Stop{
			PrimaryIdentifier: originStopID,
		})

		departureTime := "N/A"
		destination := journey["DestinationDisplay"]
		originStop := "N/A"
		if stop != nil {
			originStop = stop.PrimaryName
		}
		platform := realtimeJourneyStop["Platform"]

		if e.Type == ctdf.EventTypeRealtimeJourneyPlatformSet {
			eventNotificationData.Message = fmt.Sprintf("The %s service to %s from %s will depart from platform %s", departureTime, destination, originStop, platform)
		} else if e.Type == ctdf.EventTypeRealtimeJourneyPlatformChanged {
			oldPlatform := eventBody["OldPlatform"]
			eventNotificationData.Message = fmt.Sprintf("The %s service to %s from %s will now be departing from platform %s instead of %s", departureTime, destination, originStop, platform, oldPlatform)
		}
	}

	return eventNotificationData
}
