package identifiers

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type SiriVM struct {
	IdentifyingInformation map[string]string
	Operator               *ctdf.Operator
	PotentialServices      []string
	CurrentTime            time.Time
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
	var services []string

	serviceName := i.IdentifyingInformation["PublishedLineName"]
	if serviceName == "" {
		serviceName = i.IdentifyingInformation["ServiceNameRef"]
	}

	servicesCollection := database.GetCollection("services")

	cursor, err := servicesCollection.Find(context.Background(), bson.M{
		"$and": bson.A{bson.M{"servicename": serviceName},
			bson.M{"operatorref": bson.M{"$in": i.Operator.OtherIdentifiers}},
		},
	})

	if err != nil {
		log.Fatal().Err(err).Msg("Failed to perform query")
	}

	for cursor.Next(context.Background()) {
		var service *ctdf.Service
		err := cursor.Decode(&service)
		if err != nil {
			log.Error().Err(err).Str("serviceName", serviceName).Msg("Failed to decode service")
		}

		services = append(services, service.PrimaryIdentifier)
	}

	serviceNameRegex, _ := regexp.Compile("^\\D+(\\d+)$")
	if len(services) == 0 {
		serviceNameMatch := serviceNameRegex.FindStringSubmatch(serviceName)

		if len(serviceNameMatch) == 2 {
			cursor, _ := servicesCollection.Find(context.Background(), bson.M{
				"$and": bson.A{bson.M{"servicename": serviceNameMatch[1]},
					bson.M{"operatorref": bson.M{"$in": i.Operator.OtherIdentifiers}},
				},
			})

			for cursor.Next(context.Background()) {
				var service *ctdf.Service
				err := cursor.Decode(&service)
				if err != nil {
					log.Error().Err(err).Str("serviceName", serviceName).Msg("Failed to decode service")
				}

				services = append(services, service.PrimaryIdentifier)
			}
		}
	}

	return services
}

func (i *SiriVM) IdentifyJourney() (string, error) {
	i.CurrentTime = time.Now()

	// Get the directly referenced Operator
	i.Operator = i.getOperator()

	if i.Operator == nil {
		return "", errors.New("Could not find referenced Operator")
	}

	// Get the relevant Services
	i.PotentialServices = i.getServices()

	if len(i.PotentialServices) == 0 {
		return "", errors.New("Could not find related Service")
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

	// First try getting Journeys by the TicketMachineJourneyCode
	if vehicleJourneyRef != "" {
		journeys = getAvailableJourneys(journeysCollection, framedVehicleJourneyDate, bson.M{
			"$and": bson.A{
				bson.M{"serviceref": bson.M{"$in": i.PotentialServices}},
				bson.M{"otheridentifiers.TicketMachineJourneyCode": vehicleJourneyRef},
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

	// If we fail with the ID codes then try with the origin & destination stops
	var journeyQuery []bson.M
	for _, service := range i.PotentialServices {
		journeyQuery = append(journeyQuery, bson.M{"$or": bson.A{
			bson.M{
				"$and": bson.A{
					bson.M{"serviceref": service},
					bson.M{"path.originstopref": i.IdentifyingInformation["OriginRef"]},
				},
			},
			bson.M{
				"$and": bson.A{
					bson.M{"serviceref": service},
					bson.M{"path.destinationstopref": i.IdentifyingInformation["DestinationRef"]},
				},
			},
		}})
	}

	journeys = getAvailableJourneys(journeysCollection, framedVehicleJourneyDate, bson.M{"$or": journeyQuery})

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

	if len(journeys) == 0 {
		return nil, errors.New("Could not find related Journeys")
	} else if len(journeys) == 1 {
		return journeys[0], nil
	} else {
		var timeFilteredJourneys []*ctdf.Journey

		// Filter based on exact time
		for _, journey := range journeys {
			originAimedDepartureTimeNoOffset, _ := time.Parse(ctdf.XSDDateTimeFormat, i.IdentifyingInformation["OriginAimedDepartureTime"])
			originAimedDepartureTime := originAimedDepartureTimeNoOffset.In(i.CurrentTime.Location())

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

				originAimedDepartureTimeNoOffset, _ := time.Parse(ctdf.XSDDateTimeFormat, i.IdentifyingInformation["OriginAimedDepartureTime"])
				originAimedDepartureTime := originAimedDepartureTimeNoOffset.In(i.CurrentTime.Location())

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
