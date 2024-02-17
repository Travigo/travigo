package events

import (
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
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

		journeyRunDate, _ := time.Parse(time.RFC3339, eventBody["JourneyRunDate"].(string))
		journeyRunDateText := journeyRunDate.Format("02/01")

		origin := journey["Path"].([]interface{})[0].(map[string]interface{})["OriginStopRef"].(string)

		destination := journey["DestinationDisplay"]
		eventNotificationData.Message = fmt.Sprintf("The %s %s to %s from %s has been cancelled.", journeyRunDateText, departureTimeText, destination, origin)

		// TODO now we need to work out why it was cancelled again
		// if eventBody["Annotations"].(map[string]interface{})["CancelledReasonText"] != nil {
		// 	eventNotificationData.Message = fmt.Sprintf("%s %s", eventNotificationData.Message, eventBody["Annotations"].(map[string]interface{})["CancelledReasonText"])
		// }
	case ctdf.EventTypeRealtimeJourneyPlatformSet, ctdf.EventTypeRealtimeJourneyPlatformChanged:
		eventNotificationData.Title = "Platform Update"

		realtimeJourney := eventBody["RealtimeJourney"].(map[string]interface{})
		journey := realtimeJourney["Journey"].(map[string]interface{})
		originStopID := eventBody["Stop"].(string)
		realtimeJourneyStop := realtimeJourney["Stops"].(map[string]interface{})[originStopID].(map[string]interface{})

		var stop *ctdf.Stop
		stop, err := dataaggregator.Lookup[*ctdf.Stop](query.Stop{
			PrimaryIdentifier: originStopID,
		})
		if err != nil {
			log.Error().Err(err).Str("stop", originStopID).Msg("Failed to lookup stop")
		}

		departureTime, _ := time.Parse(time.RFC3339, journey["DepartureTime"].(string))
		departureTimeText := departureTime.Format("15:04")
		destination := journey["DestinationDisplay"]
		originStop := originStopID
		if stop != nil {
			originStop = stop.PrimaryName
		}
		platform := realtimeJourneyStop["Platform"]

		if e.Type == ctdf.EventTypeRealtimeJourneyPlatformSet {
			eventNotificationData.Message = fmt.Sprintf("The %s service to %s from %s will depart from platform %s", departureTimeText, destination, originStop, platform)
		} else if e.Type == ctdf.EventTypeRealtimeJourneyPlatformChanged {
			oldPlatform := eventBody["OldPlatform"]
			eventNotificationData.Message = fmt.Sprintf("The %s service to %s from %s will now be departing from platform %s instead of %s", departureTimeText, destination, originStop, platform, oldPlatform)
		}
	}

	return eventNotificationData
}
