package journeyidentifier

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func getOperator(identifyingInformation map[string]string) *ctdf.Operator {
	var operator *ctdf.Operator
	operatorRef := identifyingInformation["OperatorRef"]
	operatorsCollection := database.GetCollection("operators")
	query := bson.M{"$or": bson.A{bson.M{"primaryidentifier": operatorRef}, bson.M{"otheridentifiers": operatorRef}}}
	operatorsCollection.FindOne(context.Background(), query).Decode(&operator)

	return operator
}

func getServices(identifyingInformation map[string]string, operator *ctdf.Operator) []string {
	var services []string

	serviceName := identifyingInformation["PublishedLineName"]
	if serviceName == "" {
		serviceName = identifyingInformation["ServiceNameRef"]
	}

	servicesCollection := database.GetCollection("services")

	cursor, _ := servicesCollection.Find(context.Background(), bson.M{
		"$and": bson.A{bson.M{"servicename": serviceName},
			bson.M{"operatorref": bson.M{"$in": operator.OtherIdentifiers}},
		},
	})

	for cursor.Next(context.TODO()) {
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
					bson.M{"operatorref": bson.M{"$in": operator.OtherIdentifiers}},
				},
			})

			for cursor.Next(context.TODO()) {
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

// The CTDF abstraction fails here are we only use siri-vm identifyinginformation
//
//	currently no other kind so is fine for now (TODO)
func IdentifyJourney(identifyingInformation map[string]string) (string, error) {
	currentTime := time.Now()

	// Get the directly referenced Operator
	operator := getOperator(identifyingInformation)

	if operator == nil {
		return "", errors.New("Could not find referenced Operator")
	}

	// Get the relevant Services
	services := getServices(identifyingInformation, operator)

	if len(services) == 0 {
		return "", errors.New("Could not find related Service")
	}

	// Get the relevant Journeys
	var framedVehicleJourneyDate time.Time
	if identifyingInformation["FramedVehicleJourneyDate"] == "" {
		framedVehicleJourneyDate = time.Now()
	} else {
		framedVehicleJourneyDate, _ = time.Parse(ctdf.YearMonthDayFormat, identifyingInformation["FramedVehicleJourneyDate"])

		// Fallback for dodgy formatted frames
		if framedVehicleJourneyDate.Year() < 2022 {
			framedVehicleJourneyDate = time.Now()
		}
	}

	journeys := []*ctdf.Journey{}

	vehicleJourneyRef := identifyingInformation["VehicleJourneyRef"]
	blockRef := identifyingInformation["BlockRef"]
	journeysCollection := database.GetCollection("journeys")

	// First try getting Journeys by the TicketMachineJourneyCode
	if vehicleJourneyRef != "" {
		journeys = getAvailableJourneys(journeysCollection, framedVehicleJourneyDate, bson.M{
			"$and": bson.A{
				bson.M{"serviceref": bson.M{"$in": services}},
				bson.M{"otheridentifiers.TicketMachineJourneyCode": vehicleJourneyRef},
			},
		})
		identifiedJourney, err := narrowJourneys(identifyingInformation, currentTime, journeys, true)
		if err == nil {
			return identifiedJourney.PrimaryIdentifier, nil
		}
	}

	// Fallback to Block Ref (incorrect usage of block ref but it kinda works)
	if blockRef != "" {
		journeys = getAvailableJourneys(journeysCollection, framedVehicleJourneyDate, bson.M{
			"$and": bson.A{
				bson.M{"serviceref": bson.M{"$in": services}},
				bson.M{"otheridentifiers.BlockNumber": blockRef},
			},
		})
		identifiedJourney, err := narrowJourneys(identifyingInformation, currentTime, journeys, true)
		if err == nil {
			return identifiedJourney.PrimaryIdentifier, nil
		}
	}

	// If we fail with the ID codes then try with the origin & destination stops
	journeyQuery := []bson.M{}
	for _, service := range services {
		journeyQuery = append(journeyQuery, bson.M{"$or": bson.A{
			bson.M{
				"$and": bson.A{
					bson.M{"serviceref": service},
					bson.M{"path.originstopref": identifyingInformation["OriginRef"]},
				},
			},
			bson.M{
				"$and": bson.A{
					bson.M{"serviceref": service},
					bson.M{"path.destinationstopref": identifyingInformation["DestinationRef"]},
				},
			},
		}})
	}

	journeys = getAvailableJourneys(journeysCollection, framedVehicleJourneyDate, bson.M{"$or": journeyQuery})

	identifiedJourney, err := narrowJourneys(identifyingInformation, currentTime, journeys, true)

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

func getAvailableJourneys(journeysCollection *mongo.Collection, framedVehicleJourneyDate time.Time, query bson.M) []*ctdf.Journey {
	journeys := []*ctdf.Journey{}

	opts := options.Find().SetProjection(bson.D{
		bson.E{Key: "_id", Value: 0},
		bson.E{Key: "otheridentifiers", Value: 0},
		bson.E{Key: "datasource", Value: 0},
		bson.E{Key: "creationdatetime", Value: 0},
		bson.E{Key: "modificationdatetime", Value: 0},
		bson.E{Key: "destinationdisplay", Value: 0},
		bson.E{Key: "path.track", Value: 0},
		bson.E{Key: "path.originactivity", Value: 0},
		bson.E{Key: "path.destinationactivity", Value: 0},
	})
	cursor, _ := journeysCollection.Find(context.Background(), query, opts)

	for cursor.Next(context.TODO()) {
		var journey *ctdf.Journey
		err := cursor.Decode(&journey)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode journey")
		}

		// if it has no availability then we'll just ignore it
		if journey.Availability != nil && journey.Availability.MatchDate(framedVehicleJourneyDate) {
			journeys = append(journeys, journey)
		}
	}

	return journeys
}

func narrowJourneys(identifyingInformation map[string]string, currentTime time.Time, journeys []*ctdf.Journey, includeAvailabilityCondition bool) (*ctdf.Journey, error) {
	journeys = ctdf.FilterIdenticalJourneys(journeys, includeAvailabilityCondition)

	if len(journeys) == 0 {
		return nil, errors.New("Could not find related Journeys")
	} else if len(journeys) == 1 {
		return journeys[0], nil
	} else {
		timeFilteredJourneys := []*ctdf.Journey{}

		// Filter based on exact time
		for _, journey := range journeys {
			originAimedDepartureTimeNoOffset, _ := time.Parse(ctdf.XSDDateTimeFormat, identifyingInformation["OriginAimedDepartureTime"])
			originAimedDepartureTime := originAimedDepartureTimeNoOffset.In(currentTime.Location())

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

				if !(journey.Path[0].OriginStopRef == identifyingInformation["OriginRef"] || journey.Path[len(journey.Path)-1].DestinationStopRef == identifyingInformation["DestinationRef"]) {
					continue
				}

				originAimedDepartureTimeNoOffset, _ := time.Parse(ctdf.XSDDateTimeFormat, identifyingInformation["OriginAimedDepartureTime"])
				originAimedDepartureTime := originAimedDepartureTimeNoOffset.In(currentTime.Location())

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
				journey, err := narrowJourneys(identifyingInformation, currentTime, journeys, false)

				return journey, err
			} else {
				return nil, errors.New("Could not narrow down to single Journey by time. Still many remaining")
			}
		}
	}
}
