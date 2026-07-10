package identifiers

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var serviceNameNumberSuffixRegex = regexp.MustCompile("^\\D+(\\d+)$")

type SiriVM struct {
	IdentifyingInformation map[string]string
	Operator               *ctdf.Operator
	PotentialServices      []string
	CurrentTime            time.Time
}

func (i *SiriVM) IdentifyServices() ([]string, error) {
	i.Operator = i.getOperator()
	if i.Operator == nil {
		return nil, errors.New("Could not find referenced Operator")
	}
	i.PotentialServices = i.getServices()
	if len(i.PotentialServices) == 0 {
		return nil, errors.New("Could not find related Service")
	}
	return i.PotentialServices, nil
}

func (i *SiriVM) getOperator() *ctdf.Operator {
	var operator *ctdf.Operator
	operatorRef := i.IdentifyingInformation["OperatorRef"]
	operatorsCollection := database.GetCollection("operators")
	query := bson.M{"$or": bson.A{bson.M{"primaryidentifier": operatorRef}, bson.M{"otheridentifiers": operatorRef}}}
	operatorsCollection.FindOne(context.Background(), query).Decode(&operator)

	return operator
}

func (i *SiriVM) getServices() []string {
	serviceName := i.IdentifyingInformation["PublishedLineName"]
	if serviceName == "" {
		serviceName = i.IdentifyingInformation["ServiceNameRef"]
	}
	if serviceName == "" || i.Operator == nil {
		return nil
	}

	servicesCollection := database.GetCollection("services")
	opts := options.Find().SetProjection(bson.D{
		bson.E{Key: "primaryidentifier", Value: 1},
		bson.E{Key: "servicename", Value: 1},
	})
	operatorRefs := append([]string{i.Operator.PrimaryIdentifier}, i.Operator.OtherIdentifiers...)
	operatorRefs = uniqueNonEmptyStrings(operatorRefs)
	cursor, err := servicesCollection.Find(context.Background(), bson.M{"operatorref": bson.M{"$in": operatorRefs}}, opts)

	if err != nil {
		log.Fatal().Err(err).Msg("Failed to perform query")
	}
	defer cursor.Close(context.Background())

	services := make([]string, 0, 4)
	for cursor.Next(context.Background()) {
		var service *ctdf.Service
		err := cursor.Decode(&service)
		if err != nil {
			log.Error().Err(err).Str("serviceName", serviceName).Msg("Failed to decode service")
		}

		if sameServiceName(service.ServiceName, serviceName) {
			services = append(services, service.PrimaryIdentifier)
		}
	}
	return uniqueNonEmptyStrings(services)
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func sameServiceName(left, right string) bool {
	normalize := func(value string) string {
		value = strings.ToUpper(strings.TrimSpace(value))
		value = strings.NewReplacer(" ", "", "-", "", "/", "").Replace(value)
		return value
	}
	if normalize(left) == normalize(right) {
		return true
	}
	match := serviceNameNumberSuffixRegex.FindStringSubmatch(strings.TrimSpace(right))
	return len(match) == 2 && normalize(left) == match[1]
}

func (i *SiriVM) IdentifyJourney() (string, error) {
	if i.CurrentTime.IsZero() {
		i.CurrentTime = time.Now()
	}
	if _, err := i.IdentifyServices(); err != nil {
		return "", err
	}

	// Get the relevant Journeys
	var framedVehicleJourneyDate time.Time
	if i.IdentifyingInformation["FramedVehicleJourneyDate"] == "" {
		framedVehicleJourneyDate = time.Now()
	} else {
		framedVehicleJourneyDate, _ = time.Parse(ctdf.YearMonthDayFormat, i.IdentifyingInformation["FramedVehicleJourneyDate"])

		// Fallback for dodgy formatted frames
		if framedVehicleJourneyDate.Year() < 2024 {
			framedVehicleJourneyDate = time.Now()
		}
	}

	var journeys []*ctdf.Journey

	vehicleJourneyRef := i.IdentifyingInformation["VehicleJourneyRef"]
	blockRef := i.IdentifyingInformation["BlockRef"]
	journeysCollection := database.GetCollection("journeys")

	// Providers use VehicleJourneyRef inconsistently: in BODS it may be a
	// GTFS trip ID, while legacy SIRI feeds commonly expose a ticket-machine ID.
	if vehicleJourneyRef != "" {
		journeys = getAvailableJourneys(journeysCollection, framedVehicleJourneyDate, bson.M{
			"$and": bson.A{
				bson.M{"serviceref": bson.M{"$in": i.PotentialServices}},
				bson.M{"$or": bson.A{
					bson.M{"otheridentifiers.GTFS-TripID": vehicleJourneyRef},
					bson.M{"otheridentifiers.TicketMachineJourneyCode": vehicleJourneyRef},
				}},
			},
		})
		identifiedJourney, err := i.narrowJourneys(journeys, true)
		if err == nil {
			return identifiedJourney.PrimaryIdentifier, nil
		}
	}

	// Fallback to Block Ref (incorrect usage of block ref but it kinda works)
	if blockRef != "" {
		journeys = getAvailableJourneys(journeysCollection, framedVehicleJourneyDate, bson.M{
			"$and": bson.A{
				bson.M{"serviceref": bson.M{"$in": i.PotentialServices}},
				bson.M{"otheridentifiers.BlockNumber": blockRef},
			},
		})
		identifiedJourney, err := i.narrowJourneys(journeys, true)
		if err == nil {
			return identifiedJourney.PrimaryIdentifier, nil
		}
	}

	// If we fail with the ID codes then use both endpoints whenever supplied.
	endpointQuery := bson.A{}
	if originRef := i.IdentifyingInformation["OriginRef"]; originRef != "" {
		endpointQuery = append(endpointQuery, bson.M{"path.originstopref": originRef})
	}
	if destinationRef := i.IdentifyingInformation["DestinationRef"]; destinationRef != "" {
		endpointQuery = append(endpointQuery, bson.M{"path.destinationstopref": destinationRef})
	}
	if len(endpointQuery) == 0 {
		return "", errors.New("Missing journey identifiers and endpoints")
	}
	journeys = getAvailableJourneys(journeysCollection, framedVehicleJourneyDate, bson.M{
		"$and": bson.A{
			bson.M{"serviceref": bson.M{"$in": i.PotentialServices}},
			bson.M{"$and": endpointQuery},
		},
	})

	identifiedJourney, err := i.narrowJourneys(journeys, true)

	if err == nil {
		return identifiedJourney.PrimaryIdentifier, nil
	} else {
		// log.Debug().Err(err).Int("length", len(journeys)).Msgf("wtf")

		// pretty.Println(identifyingInformation)
		// for _, journey := range journeys {
		// 	log.Debug().Msg(journey.PrimaryIdentifier)
		// }
		// log.Fatal().Err(err).Msgf("OKAY")

		return "", err
	}
}

func (i *SiriVM) narrowJourneys(journeys []*ctdf.Journey, includeAvailabilityCondition bool) (*ctdf.Journey, error) {
	journeys = ctdf.FilterIdenticalJourneys(journeys, includeAvailabilityCondition)
	if direction := i.IdentifyingInformation["DirectionRef"]; direction != "" {
		directional := make([]*ctdf.Journey, 0, len(journeys))
		for _, journey := range journeys {
			if strings.EqualFold(journey.Direction, direction) {
				directional = append(directional, journey)
			}
		}
		if len(directional) > 0 {
			journeys = directional
		}
	}

	if len(journeys) == 0 {
		return nil, errors.New("Could not find related Journeys")
	} else if len(journeys) == 1 {
		return journeys[0], nil
	} else {
		var timeFilteredJourneys []*ctdf.Journey
		originAimedDepartureTimeNoOffset, _ := time.Parse(ctdf.XSDDateTimeFormat, i.IdentifyingInformation["OriginAimedDepartureTime"])
		originAimedDepartureTime := originAimedDepartureTimeNoOffset.In(i.CurrentTime.Location())

		// Filter based on exact time
		for _, journey := range journeys {
			if journey.DepartureTime.Hour() == originAimedDepartureTime.Hour() && journey.DepartureTime.Minute() == originAimedDepartureTime.Minute() {
				timeFilteredJourneys = append(timeFilteredJourneys, journey)
			}
		}

		// If fail exact time then give a few minute on each side a try if at least one of the start/end stops match
		allowedMinuteOffset := 5
		if len(timeFilteredJourneys) == 0 {
			for _, journey := range journeys {
				// Skip check if none of the start/end stops match
				if len(journey.Path) == 0 {
					continue
				}

				if !(journey.Path[0].OriginStopRef == i.IdentifyingInformation["OriginRef"] || journey.Path[len(journey.Path)-1].DestinationStopRef == i.IdentifyingInformation["DestinationRef"]) {
					continue
				}

				originAimedDepartureTimeDayMinutes := (originAimedDepartureTime.Hour() * 60) + originAimedDepartureTime.Minute()
				journeyDepartureTimeDayMinutes := (journey.DepartureTime.Hour() * 60) + journey.DepartureTime.Minute()
				dayMinuteDiff := originAimedDepartureTimeDayMinutes - journeyDepartureTimeDayMinutes

				if dayMinuteDiff <= allowedMinuteOffset && dayMinuteDiff >= (allowedMinuteOffset*-1) {
					timeFilteredJourneys = append(timeFilteredJourneys, journey)
				}
			}
		}

		if len(timeFilteredJourneys) == 0 {
			return nil, errors.New("Could not narrow down to single Journey with departure time. Now zero")
		} else if len(timeFilteredJourneys) == 1 {
			return timeFilteredJourneys[0], nil
		} else {
			if includeAvailabilityCondition {
				// Try again but ignore availability conidition in hash
				journey, err := i.narrowJourneys(journeys, false)

				return journey, err
			} else {
				return nil, errors.New("Could not narrow down to single Journey by time. Still many remaining")
			}
		}
	}
}
