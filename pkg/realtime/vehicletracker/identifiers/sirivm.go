package identifiers

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var serviceNameNumberSuffixRegex = regexp.MustCompile("^\\D+(\\d+)$")

var (
	errSiriOperator = errors.New("Could not find referenced Operator")
	errSiriService  = errors.New("Could not find related Service")
	errSiriMissing  = errors.New("Missing journey identifiers and endpoints")
	errSiriJourney  = errors.New("Could not find related Journeys")
	errSiriNoTime   = errors.New("Could not narrow down to single Journey with departure time. Now zero")
	errSiriMany     = errors.New("Could not narrow down to single Journey by time. Still many remaining")
)

func isSiriNoMatch(err error) bool {
	switch err {
	case errSiriOperator, errSiriService, errSiriMissing, errSiriJourney, errSiriNoTime, errSiriMany:
		return true
	}
	return false
}

type SiriVM struct {
	IdentifyingInformation map[string]string
	CurrentTime            time.Time
	SourceDataset          string
}

func (i *SiriVM) IdentifyServices() ([]string, error) {
	started := time.Now()
	defer func() { telemetry.Observe(telemetry.SiriServices, time.Since(started)) }()

	info := i.IdentifyingInformation
	serviceName := i.serviceName()
	key := siriCacheKey(info["LinkedDataset"], info["OperatorRef"], serviceName)
	return siriServices.get(key, func() ([]string, error) {
		operator, err := siriOperators.get(info["OperatorRef"], func() (*ctdf.Operator, error) {
			return getSiriOperator(info["OperatorRef"])
		})
		if err != nil {
			return nil, err
		}
		return getSiriServices(operator, serviceName)
	})
}

func (i *SiriVM) serviceName() string {
	if name := i.IdentifyingInformation["PublishedLineName"]; name != "" {
		return name
	}
	return i.IdentifyingInformation["ServiceNameRef"]
}

func getSiriOperator(operatorRef string) (*ctdf.Operator, error) {
	var operator *ctdf.Operator
	operatorsCollection := database.GetCollection("operators")
	query := bson.M{"$or": bson.A{bson.M{"primaryidentifier": operatorRef}, bson.M{"otheridentifiers": operatorRef}}}
	opts := options.FindOne().SetProjection(bson.M{"primaryidentifier": 1, "otheridentifiers": 1})
	if err := operatorsCollection.FindOne(context.Background(), query, opts).Decode(&operator); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errSiriOperator
		}
		return nil, err
	}
	return operator, nil
}

func getSiriServices(operator *ctdf.Operator, serviceName string) ([]string, error) {
	if serviceName == "" {
		return nil, errSiriService
	}

	servicesCollection := database.GetCollection("services")
	opts := options.Find().SetProjection(bson.D{
		bson.E{Key: "primaryidentifier", Value: 1},
		bson.E{Key: "servicename", Value: 1},
		bson.E{Key: "publisherservicename", Value: 1},
	})
	operatorRefs := append([]string{operator.PrimaryIdentifier}, operator.OtherIdentifiers...)
	operatorRefs = uniqueNonEmptyStrings(operatorRefs)
	// Performance Review [Set J] [PR145] [P2]: Identification misses load every operator service
	// and filter names in Go. Use an indexed normalised service name with operator,
	// or bounded operator/name caching with timetable-aware invalidation. Preserve
	// operator aliases and numeric-suffix matching; measure cold starts and bursts
	// of new journeys rather than assuming every GPS update takes this path.
	cursor, err := servicesCollection.Find(context.Background(), bson.M{"operatorref": bson.M{"$in": operatorRefs}}, opts)

	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())

	services := make([]string, 0, 4)
	for cursor.Next(context.Background()) {
		var service *ctdf.Service
		err := cursor.Decode(&service)
		if err != nil {
			return nil, err
		}

		if sameServiceName(service.LineName(), serviceName) {
			services = append(services, service.PrimaryIdentifier)
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	services = uniqueNonEmptyStrings(services)
	if len(services) == 0 {
		return nil, errSiriService
	}
	slices.Sort(services)
	return services, nil
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

	info := i.IdentifyingInformation
	aimed, aimedErr := time.Parse(ctdf.XSDDateTimeFormat, info["OriginAimedDepartureTime"])
	aimed = aimed.In(i.CurrentTime.Location())
	aimedClock := aimed.Format("15:04")
	var exactDeparture time.Time
	if aimedErr != nil {
		aimedClock = "invalid:" + aimedClock
	} else if aimedClock != "00:00" {
		// A missing timetable departure decodes as midnight.
		exactDeparture = aimed
	}
	key := siriCacheKey(i.SourceDataset, info["LinkedDataset"], framedVehicleJourneyDate.Format(ctdf.YearMonthDayFormat),
		info["OperatorRef"], i.serviceName(), info["VehicleJourneyRef"],
		info["BlockRef"], info["OriginRef"], info["DestinationRef"], info["DirectionRef"],
		aimedClock)
	return siriJourneys.get(key, func() (string, error) {
		return i.findJourney(framedVehicleJourneyDate, exactDeparture)
	})
}

func (i *SiriVM) findJourney(framedVehicleJourneyDate, exactDeparture time.Time) (string, error) {
	services, err := i.IdentifyServices()
	if err != nil {
		return "", err
	}
	vehicleJourneyRef := i.IdentifyingInformation["VehicleJourneyRef"]
	blockRef := i.IdentifyingInformation["BlockRef"]
	journeysCollection := database.GetCollection("journeys")

	// Providers use VehicleJourneyRef inconsistently: in BODS it may be a
	// GTFS trip ID, while legacy SIRI feeds commonly expose a ticket-machine ID.
	if vehicleJourneyRef != "" {
		identifiedJourney, err := i.findReferencedJourney(journeysCollection, framedVehicleJourneyDate, services, vehicleJourneyRef)
		if err == nil {
			return identifiedJourney.PrimaryIdentifier, nil
		}
		if !isSiriNoMatch(err) {
			return "", err
		}
	}

	// Fallback to Block Ref (incorrect usage of block ref but it kinda works)
	if blockRef != "" {
		started := time.Now()
		candidates, lookupErr := getAvailableJourneys(journeysCollection, framedVehicleJourneyDate, bson.M{
			"serviceref":                   bson.M{"$in": services},
			"otheridentifiers.BlockNumber": blockRef,
		}, time.Time{})
		identifiedJourney, err := i.selectJourney(telemetry.SiriBlockRef, time.Since(started), candidates, lookupErr, time.Time{})
		if err == nil {
			return identifiedJourney.PrimaryIdentifier, nil
		}
		if !isSiriNoMatch(err) {
			return "", err
		}
	}

	// If we fail with the ID codes then use both endpoints whenever supplied.
	query := bson.M{}
	if originRef := i.IdentifyingInformation["OriginRef"]; originRef != "" {
		query["path.originstopref"] = originRef
	}
	if destinationRef := i.IdentifyingInformation["DestinationRef"]; destinationRef != "" {
		query["path.destinationstopref"] = destinationRef
	}
	if len(query) == 0 {
		return "", errSiriMissing
	}
	query["serviceref"] = bson.M{"$in": services}
	started := time.Now()
	candidates, lookupErr := getAvailableJourneys(journeysCollection, framedVehicleJourneyDate, query, exactDeparture)
	identifiedJourney, err := i.selectJourney(telemetry.SiriStops, time.Since(started), candidates, lookupErr, exactDeparture)

	if err != nil {
		return "", err
	}
	return identifiedJourney.PrimaryIdentifier, nil
}

func (i *SiriVM) findReferencedJourney(collection *mongo.Collection, date time.Time, services []string, ref string) (*ctdf.Journey, error) {
	key := siriCacheKey(append([]string{ref}, services...)...)
	if _, ok := siriReferenceMisses.lookup(key); ok {
		return nil, errSiriJourney
	}

	// Query each indexed field independently, then consider both sets together.
	// A unique GTFS match alone cannot rule out a conflicting ticket-machine match.
	started := time.Now()
	var combined siriJourneyCandidates
	var lookupErr error
	for _, field := range []string{"otheridentifiers.GTFS-TripID", "otheridentifiers.TicketMachineJourneyCode"} {
		candidates, err := getAvailableJourneys(collection, date, bson.M{
			"serviceref": bson.M{"$in": services},
			field:        ref,
		}, time.Time{})
		combined.count += candidates.count
		combined.available = append(combined.available, candidates.available...)
		if err != nil {
			lookupErr = err
			break
		}
	}
	if lookupErr == nil && combined.count == 0 {
		siriReferenceMisses.store(key, struct{}{}, nil)
	}
	return i.selectJourney(telemetry.SiriJourneyRef, time.Since(started), combined, lookupErr, time.Time{})
}

func (i *SiriVM) selectJourney(fallback telemetry.SiriFallback, lookup time.Duration, candidates siriJourneyCandidates, lookupErr error, exactDeparture time.Time) (*ctdf.Journey, error) {
	started := time.Now()
	if lookupErr != nil {
		telemetry.ObserveSiriFallback(fallback, lookup, 0, candidates.count, len(candidates.available), false)
		return nil, lookupErr
	}
	journey := i.exactDepartureMatch(candidates, exactDeparture)
	var err error
	if journey == nil {
		journey, err = i.narrowJourneys(candidates.available, true)
	}
	telemetry.ObserveSiriFallback(fallback, lookup, time.Since(started), candidates.count, len(candidates.available), err == nil)
	return journey, err
}

func (i *SiriVM) exactDepartureMatch(candidates siriJourneyCandidates, aimed time.Time) *ctdf.Journey {
	const candidateLimit = 16
	if candidates.exactCount == 0 || candidates.exactCount >= candidateLimit {
		return nil
	}
	var match *ctdf.Journey
	direction := i.IdentifyingInformation["DirectionRef"]
	for _, journey := range candidates.available {
		if !sameDepartureMinute(journey.DepartureTime.UTC(), aimed) {
			continue
		}
		if !sameDepartureMinute(journey.DepartureTime, aimed) || journey.PrimaryIdentifier == "" {
			return nil
		}
		if direction != "" && !strings.EqualFold(journey.Direction, direction) {
			continue
		}
		if match != nil {
			return nil
		}
		match = journey
	}
	return match
}

func (i *SiriVM) narrowJourneys(journeys []*ctdf.Journey, includeAvailabilityCondition bool) (*ctdf.Journey, error) {
	// Performance Review [Set J] [PR146] [P2] (tracker): Deduplication hashes every path and formats
	// three timestamps per leg before direction/time narrowing. Ambiguous results
	// recurse and hash the candidate set again without availability. Cache both
	// fingerprints by timetable version, or carry precomputed identification hashes
	// with lightweight candidates. Any reordering of filters must preserve duplicate
	// representative selection and the existing direction/time fallback semantics.
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
		return nil, errSiriJourney
	} else if len(journeys) == 1 {
		return journeys[0], nil
	} else {
		var timeFilteredJourneys []*ctdf.Journey
		originAimedDepartureTimeNoOffset, _ := time.Parse(ctdf.XSDDateTimeFormat, i.IdentifyingInformation["OriginAimedDepartureTime"])
		originAimedDepartureTime := originAimedDepartureTimeNoOffset.In(i.CurrentTime.Location())

		// Filter based on exact time
		for _, journey := range journeys {
			if sameDepartureMinute(journey.DepartureTime, originAimedDepartureTime) {
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
			return nil, errSiriNoTime
		} else if len(timeFilteredJourneys) == 1 {
			return timeFilteredJourneys[0], nil
		} else {
			if includeAvailabilityCondition {
				// Try again but ignore availability conidition in hash
				journey, err := i.narrowJourneys(journeys, false)

				return journey, err
			} else {
				return nil, errSiriMany
			}
		}
	}
}
