package events

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/util"
)

type serviceAlertReference struct {
	kind       string
	identifier string
	date       string
}

func matchingServiceAlertReference(matchedIdentifiers []interface{}, values ctdf.UserNotificationSubscriptionValues) serviceAlertReference {
	for _, reference := range notificationReferenceCandidates(values) {
		for _, value := range matchedIdentifiers {
			identifier, ok := value.(string)
			if !ok {
				continue
			}
			if identifier == reference.identifier {
				return serviceAlertReference{kind: reference.kind, identifier: identifier}
			}
			if reference.kind == "journey" && strings.HasPrefix(identifier, "DAYINSTANCEOF:") && strings.HasSuffix(identifier, ":"+reference.identifier) {
				parts := strings.SplitN(identifier, ":", 3)
				if len(parts) == 3 {
					return serviceAlertReference{kind: reference.kind, identifier: parts[2], date: dayInstanceDate(parts[1])}
				}
			}
		}
	}

	return serviceAlertReference{}
}

type notificationReferenceCandidate struct {
	kind       string
	identifier string
}

func notificationReferenceCandidates(values ctdf.UserNotificationSubscriptionValues) []notificationReferenceCandidate {
	candidates := []notificationReferenceCandidate{}
	seen := map[string]bool{}
	appendReferences := func(kind string, values []string) {
		for _, value := range values {
			key := kind + "\x00" + value
			if value == "" || seen[key] {
				continue
			}
			seen[key] = true
			candidates = append(candidates, notificationReferenceCandidate{kind: kind, identifier: value})
		}
	}
	appendReferences("stop", append([]string{values.StopRef}, values.StopRefs...))
	appendReferences("service", append([]string{values.ServiceRef}, values.ServiceRefs...))
	appendReferences("journey", append([]string{values.JourneyRef}, values.JourneyRefs...))
	return candidates
}

func matchingServiceAlertIdentifier(matchedIdentifiers []interface{}, values ctdf.UserNotificationSubscriptionValues) string {
	return matchingServiceAlertReference(matchedIdentifiers, values).identifier
}

func dayInstanceDate(value string) string {
	for _, layout := range []string{"2006-01-02", "20060102"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.Format("2006-01-02")
		}
	}

	return ""
}

func notificationPathIdentifier(identifier string) string {
	return strings.NewReplacer("%3A", ":", "%3a", ":").Replace(url.PathEscape(identifier))
}

func notificationTargetURL(e *ctdf.Event, notificationMatcher ctdf.UserNotificationSubscription) string {
	if e == nil {
		return ""
	}

	eventBody, ok := e.Body.(map[string]interface{})
	if !ok {
		return ""
	}

	if e.Type == ctdf.EventTypeServiceAlertCreated {
		matchedIdentifiers, _ := eventBody["MatchedIdentifiers"].([]interface{})
		reference := matchingServiceAlertReference(matchedIdentifiers, notificationMatcher.Values)
		if reference.identifier == "" {
			return ""
		}

		path := ""
		switch reference.kind {
		case "stop":
			path = "/stops/" + notificationPathIdentifier(reference.identifier)
		case "service":
			path = "/services/" + notificationPathIdentifier(reference.identifier)
		case "journey":
			path = "/journeys/" + notificationPathIdentifier(reference.identifier)
			if reference.date != "" {
				path += "?date=" + url.QueryEscape(reference.date)
			}
		}

		return path
	}

	journeyBody := eventBody
	if nested, ok := eventBody["RealtimeJourney"].(map[string]interface{}); ok {
		journeyBody = nested
	}

	journey, _ := journeyBody["Journey"].(map[string]interface{})
	journeyIdentifier, _ := journey["PrimaryIdentifier"].(string)
	if journeyIdentifier == "" {
		journeyIdentifier, _ = journeyBody["PrimaryIdentifier"].(string)
	}
	if journeyIdentifier == "" {
		return ""
	}

	path := "/journeys/" + notificationPathIdentifier(journeyIdentifier)
	if runDate, _ := journeyBody["JourneyRunDate"].(string); runDate != "" {
		if parsed, err := time.Parse(time.RFC3339, runDate); err == nil {
			path += "?date=" + url.QueryEscape(parsed.Format("2006-01-02"))
		}
	}

	return path
}

func serviceAlertIdentifierDisplay(identifier string, values ctdf.UserNotificationSubscriptionValues) string {
	if identifier != "" && containsNotificationReference(identifier, append([]string{values.StopRef}, values.StopRefs...)) {
		stop, err := dataaggregator.Lookup[*ctdf.Stop](query.Stop{Identifier: identifier})
		if err != nil {
			log.Error().Err(err).Str("stop", identifier).Msg("Failed to lookup service alert stop")
		}
		if stop != nil && stop.PrimaryName != "" {
			return stop.PrimaryName
		}
	}

	if identifier != "" && containsNotificationReference(identifier, append([]string{values.ServiceRef}, values.ServiceRefs...)) {
		service, err := dataaggregator.Lookup[*ctdf.Service](query.Service{PrimaryIdentifier: identifier})
		if err != nil {
			log.Error().Err(err).Str("service", identifier).Msg("Failed to lookup service alert service")
		}
		if service != nil && service.ServiceName != "" {
			return service.ServiceName
		}
	}

	if identifier != "" && containsNotificationReference(identifier, append([]string{values.JourneyRef}, values.JourneyRefs...)) {
		journey, err := dataaggregator.Lookup[*ctdf.Journey](query.Journey{PrimaryIdentifier: identifier})
		if err != nil {
			log.Error().Err(err).Str("journey", identifier).Msg("Failed to lookup service alert journey")
		}
		if journey != nil {
			if journey.OriginDisplay != "" && journey.DestinationDisplay != "" {
				return fmt.Sprintf("%s → %s", journey.OriginDisplay, journey.DestinationDisplay)
			}
			if journey.DestinationDisplay != "" {
				return fmt.Sprintf("Journey to %s", journey.DestinationDisplay)
			}
			if journey.OriginDisplay != "" {
				return journey.OriginDisplay
			}
		}
	}

	return identifier
}

func containsNotificationReference(identifier string, references []string) bool {
	for _, reference := range references {
		if identifier == reference {
			return true
		}
	}
	return false
}

func realtimeJourneyDestination(eventBody map[string]interface{}) string {
	if nested, ok := eventBody["RealtimeJourney"].(map[string]interface{}); ok {
		eventBody = nested
	}
	journey, _ := eventBody["Journey"].(map[string]interface{})
	destination, _ := journey["DestinationDisplay"].(string)
	return destination
}

func GetNotificationData(e *ctdf.Event, notificationMatcher ctdf.UserNotificationSubscription) ctdf.EventNotificationData {
	eventNotificationData := ctdf.EventNotificationData{}

	eventBody := e.Body.(map[string]interface{})

	switch e.Type {
	case ctdf.EventTypeServiceAlertCreated:
		matchedIdentifiers, _ := eventBody["MatchedIdentifiers"].([]interface{})
		alertScope := matchingServiceAlertIdentifier(matchedIdentifiers, notificationMatcher.Values)
		alertScope = serviceAlertIdentifierDisplay(alertScope, notificationMatcher.Values)

		eventNotificationData.Title = strings.Join(util.CamelCaseSplit(eventBody["AlertType"].(string)), " ")
		if alertScope == "" {
			eventNotificationData.Message = eventBody["Text"].(string)
		} else {
			eventNotificationData.Message = fmt.Sprintf("%s\n%s", alertScope, eventBody["Text"].(string))
		}

		title := eventBody["Title"].(string)
		if title != "" {
			eventNotificationData.Title = title
		}
	case ctdf.EventTypeRealtimeJourneyCreated:
		eventNotificationData.Title = "Journey created"
		destination := realtimeJourneyDestination(eventBody)
		if destination == "" {
			eventNotificationData.Message = "Live data is now available for this journey."
		} else {
			eventNotificationData.Message = fmt.Sprintf("Live data is now available for the service to %s.", destination)
		}
	case ctdf.EventTypeRealtimeJourneyActivelyTracked:
		eventNotificationData.Title = "Live tracking started"
		destination := realtimeJourneyDestination(eventBody)
		if destination == "" {
			eventNotificationData.Message = "Live tracking has started for this journey."
		} else {
			eventNotificationData.Message = fmt.Sprintf("Live tracking has started for the service to %s.", destination)
		}
	case ctdf.EventTypeRealtimeJourneyLocationTextChanged:
		eventNotificationData.Title = "Location changed"
		destination := realtimeJourneyDestination(eventBody)
		locationDescription, _ := eventBody["VehicleLocationDescription"].(string)
		if locationDescription == "" {
			if destination == "" {
				eventNotificationData.Message = "The live location description for this journey was cleared."
			} else {
				eventNotificationData.Message = fmt.Sprintf("The live location description for the service to %s was cleared.", destination)
			}
		} else if destination == "" {
			eventNotificationData.Message = fmt.Sprintf("The journey is now at %s.", locationDescription)
		} else {
			eventNotificationData.Message = fmt.Sprintf("The service to %s is now at %s.", destination, locationDescription)
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
	case ctdf.EventTypeRealtimeJourneyNextStopChanged:
		eventNotificationData.Title = "Next stop changed"

		journey, _ := eventBody["Journey"].(map[string]interface{})
		destination, _ := journey["DestinationDisplay"].(string)
		nextStopRef, _ := eventBody["NextStopRef"].(string)
		nextStopName := nextStopRef
		if nextStop, ok := eventBody["NextStop"].(map[string]interface{}); ok {
			if primaryName, ok := nextStop["PrimaryName"].(string); ok && primaryName != "" {
				nextStopName = primaryName
			}
		}
		if nextStopName == nextStopRef && nextStopRef != "" {
			stop, err := dataaggregator.Lookup[*ctdf.Stop](query.Stop{Identifier: nextStopRef})
			if err != nil {
				log.Error().Err(err).Str("stop", nextStopRef).Msg("Failed to lookup next stop")
			}
			if stop != nil {
				nextStopName = stop.PrimaryName
			}
		}

		if nextStopName == "" {
			eventNotificationData.Message = fmt.Sprintf("The service to %s has no further stops.", destination)
		} else {
			eventNotificationData.Message = fmt.Sprintf("The service to %s is now heading to %s.", destination, nextStopName)
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
	case ctdf.EventTypeRealtimeJourneyDelayed:
		eventNotificationData.Title = "Journey delayed"
		destination := realtimeJourneyDestination(eventBody)
		delaySeconds, _ := eventBody["DelaySeconds"].(float64)
		delay := time.Duration(delaySeconds * float64(time.Second))
		if delay < time.Minute {
			eventNotificationData.Message = "A service in this commute is now running late."
		} else if destination == "" {
			eventNotificationData.Message = fmt.Sprintf("A service in this commute is now %d minutes late.", int(delay.Round(time.Minute)/time.Minute))
		} else {
			eventNotificationData.Message = fmt.Sprintf("The service to %s is now %d minutes late.", destination, int(delay.Round(time.Minute)/time.Minute))
		}
	}

	return eventNotificationData
}
