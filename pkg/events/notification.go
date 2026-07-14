package events

import (
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/util"
)

func GetNotificationData(e *ctdf.Event) ctdf.EventNotificationData {
	eventNotificationData := ctdf.EventNotificationData{}

	eventBody := e.Body.(map[string]interface{})

	switch e.Type {
	case ctdf.EventTypeServiceAlertCreated:
		matchedIdentifiers := eventBody["MatchedIdentifiers"].([]interface{})
		matchedIdentifiersStrings := make([]string, len(matchedIdentifiers))
		for i, identifier := range matchedIdentifiers {
			identifierString := identifier.(string)
			identifierStringSplit := strings.Split(identifierString, ":")
			if identifierStringSplit[0] == "DAYINSTANCEOF" {
				identifierString = strings.Join(identifierStringSplit[2:], ":")
			}
			matchedIdentifiersStrings[i] = identifierString
		}

		alertScope := strings.Join(matchedIdentifiersStrings, ", ") // TODO needs to work out actual name

		eventNotificationData.Title = strings.Join(util.CamelCaseSplit(eventBody["AlertType"].(string)), " ")
		eventNotificationData.Message = fmt.Sprintf("%s\n%s", alertScope, eventBody["Text"].(string))

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
	case ctdf.EventTypeRealtimeJourneyOverlayCreated:
		eventNotificationData.Title = "Journey replaced"
		replacementRef, _ := eventBody["ReplacedByJourneyRef"].(string)
		if replacementRef == "" {
			eventNotificationData.Message = "A journey has been replaced by an amended service."
		} else {
			eventNotificationData.Message = fmt.Sprintf("A journey has been replaced by %s.", replacementRef)
		}
	case ctdf.EventTypeRealtimeJourneyPlatformSet, ctdf.EventTypeRealtimeJourneyPlatformChanged:
		eventNotificationData.Title = "Platform Update"

		realtimeJourney := eventBody["RealtimeJourney"].(map[string]interface{})
		journey := realtimeJourney["Journey"].(map[string]interface{})
		originStopID := eventBody["Stop"].(string)

		var stop *ctdf.Stop
		stop, err := dataaggregator.Lookup[*ctdf.Stop](query.Stop{
			Identifier: originStopID,
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
		platform := eventBody["NewPlatform"]

		if e.Type == ctdf.EventTypeRealtimeJourneyPlatformSet {
			eventNotificationData.Message = fmt.Sprintf("The %s service to %s from %s will depart from platform %s", departureTimeText, destination, originStop, platform)
		} else if e.Type == ctdf.EventTypeRealtimeJourneyPlatformChanged {
			oldPlatform := eventBody["OldPlatform"]
			eventNotificationData.Message = fmt.Sprintf("The %s service to %s from %s will now be departing from platform %s instead of %s", departureTimeText, destination, originStop, platform, oldPlatform)
		}
	}

	return eventNotificationData
}
