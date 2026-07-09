package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/transforms"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func StopsRouter(router fiber.Router) {
	router.Get("/", listStops)

	router.Get("/search", searchStops)

	router.Get("/:identifier/osm", getStopOSM)
	router.Get("/:identifier/detailed", getStopDetailed)
	router.Get("/:identifier", getStop)
	router.Get("/:identifier/departures", getStopDepartures)
}

func listStops(c *fiber.Ctx) error {
	boundsQuery, err := getLocationQuery(c)

	point := c.Query("point")

	if err != nil {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// TODO get these all working with the 1 field/index
	locationField := "location.coordinates"
	if point != "" {
		locationField = "location"
	}

	var stops []*ctdf.Stop

	stopsCollection := database.GetCollection("stops")

	bsonQuery := bson.M{locationField: boundsQuery}

	transportTypeFilter := c.Query("transport_type")
	if transportTypeFilter != "" {
		transportType := strings.Split(transportTypeFilter, ",")

		bsonQuery = bson.M{
			"$and": bson.A{
				bson.M{"transporttypes": bson.M{"$in": transportType}},
				bson.M{locationField: boundsQuery},
			},
		}
	}

	opts := options.Find().SetProjection(bson.D{
		bson.E{Key: "_id", Value: 0},
		bson.E{Key: "otheridentifiers", Value: 0},
		bson.E{Key: "datasource", Value: 0},
		bson.E{Key: "creationdatetime", Value: 0},
		bson.E{Key: "modificationdatetime", Value: 0},
		bson.E{Key: "associations", Value: 0},
	})

	cursor, err := stopsCollection.Find(context.Background(), bsonQuery, opts)
	if err != nil {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	for cursor.Next(context.Background()) {
		var stop *ctdf.Stop
		err := cursor.Decode(&stop)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode Stop")
		}

		stops = append(stops, stop)
	}

	const maxConcurrentServiceLookups = 8
	workerCount := maxConcurrentServiceLookups
	if len(stops) < workerCount {
		workerCount = len(stops)
	}

	jobs := make(chan *ctdf.Stop)
	wg := sync.WaitGroup{}
	for worker := 0; worker < workerCount; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for stop := range jobs {
				services, _ := dataaggregator.Lookup[[]*ctdf.Service](query.ServicesByStop{
					Stop: stop,
				})
				transforms.Transform(services, 1)
				stop.Services = services
			}
		}()
	}
	for _, stop := range stops {
		jobs <- stop
	}
	close(jobs)
	wg.Wait()

	reducedStops, _ := sheriff.Marshal(&sheriff.Options{
		Groups: []string{"basic"},
	}, stops)

	c.JSON(reducedStops)
	return nil
}

func getStop(c *fiber.Ctx) error {
	identifier := c.Params("identifier")
	isLLM := strings.ToLower(c.Query("isllm"))

	var stop *ctdf.Stop
	stop, err := dataaggregator.Lookup[*ctdf.Stop](query.Stop{
		Identifier: identifier,
	})

	if err != nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	} else {
		stop.Services, _ = dataaggregator.Lookup[[]*ctdf.Service](query.ServicesByStop{
			Stop: stop,
		})

		transforms.Transform(stop, 3)

		reduceGroupsName := []string{"basic", "detailed"}
		if isLLM == "true" {
			reduceGroupsName = []string{"stop-llm"}
		}

		reducedStop, _ := sheriff.Marshal(&sheriff.Options{
			Groups: reduceGroupsName,
		}, stop)

		return c.JSON(reducedStop)
	}
}

func getStopOSM(c *fiber.Ctx) error {
	identifier := c.Params("identifier")
	forceRefresh := strings.ToLower(c.Query("force_refresh")) == "true"
	radiusMetres := 0

	if radiusMetresQuery := c.Query("radius_metres"); radiusMetresQuery != "" {
		var err error
		radiusMetres, err = strconv.Atoi(radiusMetresQuery)
		if err != nil {
			c.SendStatus(fiber.StatusBadRequest)
			return c.JSON(fiber.Map{
				"error": "Parameter radius_metres should be an integer",
			})
		}
	}

	stop, err := dataaggregator.Lookup[*ctdf.Stop](query.Stop{
		Identifier: identifier,
	})
	if err != nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	osmStop, err := dataaggregator.Lookup[*ctdf.OSMStop](query.OSMStop{
		Stop:         stop,
		ForceRefresh: forceRefresh,
		RadiusMetres: radiusMetres,
	})
	if err != nil {
		c.SendStatus(fiber.StatusBadGateway)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	reducedOSMStop, err := sheriff.Marshal(&sheriff.Options{
		Groups: []string{"basic", "detailed", "internal"},
	}, osmStop)
	if err != nil {
		c.SendStatus(fiber.StatusInternalServerError)
		return c.JSON(fiber.Map{
			"error": "Sherrif could not reduce OSMStop",
		})
	}

	return c.JSON(reducedOSMStop)
}

func getStopDetailed(c *fiber.Ctx) error {
	identifier := c.Params("identifier")

	var stop *ctdf.StopDetailed
	stop, err := dataaggregator.Lookup[*ctdf.StopDetailed](query.StopDetailed{
		PrimaryIdentifier: identifier,
	})

	if err != nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	reducedStop, _ := sheriff.Marshal(&sheriff.Options{
		Groups: []string{"basic", "detailed"},
	}, stop)

	return c.JSON(reducedStop)
}

func getStopDepartures(c *fiber.Ctx) error {
	requestStart := time.Now()
	stopIdentifier := c.Params("identifier")
	count, err := strconv.Atoi(c.Query("count", "25"))
	startDateTimeString := c.Query("datetime")
	isLLM := strings.ToLower(c.Query("isllm"))

	if err != nil {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": "Parameter count should be an integer",
		})
	}

	stopLookupStart := time.Now()
	var stop *ctdf.Stop
	stop, err = dataaggregator.Lookup[*ctdf.Stop](query.Stop{
		Identifier: stopIdentifier,
	})
	stopLookupDuration := time.Since(stopLookupStart)

	if err != nil {
		c.SendStatus(fiber.StatusNotFound)
		return c.JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	var startDateTime time.Time
	if startDateTimeString == "" {
		stopTimezone, _ := time.LoadLocation(stop.Timezone)

		startDateTime = time.Now().In(stopTimezone)
	} else {
		startDateTime, err = time.Parse(time.RFC3339, startDateTimeString)

		if err != nil {
			c.SendStatus(fiber.StatusBadRequest)
			return c.JSON(fiber.Map{
				"error":    "Parameter datetime should be an RFS3339/ISO8601 datetime",
				"detailed": err,
			})
		}
	}

	departureLookupStart := time.Now()
	var departureBoard []*ctdf.DepartureBoard
	departureBoard, err = dataaggregator.Lookup[[]*ctdf.DepartureBoard](query.DepartureBoard{
		Stop:          stop,
		Count:         count,
		StartDateTime: startDateTime,
	})
	departureLookupDuration := time.Since(departureLookupStart)
	beforeSortCount := len(departureBoard)

	// Sort departures by DepartureBoard time
	sortStart := time.Now()
	sort.Slice(departureBoard, func(i, j int) bool {
		return departureBoard[i].Time.Before(departureBoard[j].Time)
	})
	sortDuration := time.Since(sortStart)

	// Once sorted cut off any records higher than our max count
	if len(departureBoard) > count {
		departureBoard = departureBoard[:count]
	}
	afterTruncateCount := len(departureBoard)

	destinationDisplayStart := time.Now()
	destinationFallbacks, destinationServiceOverridesApplied := resolveDepartureBoardDestinationDisplays(departureBoard)
	destinationDisplayDuration := time.Since(destinationDisplayStart)

	currentTime := time.Now()
	// Transforming the whole document is incredibly ineffecient
	// Instead just transform the Operator & Service as those are the key values
	nilDepartureItems, transformedOperators, transformedServices, reusedOperators, reusedServices := transformDepartureBoardReferences(departureBoard)
	transformDuration := time.Since(currentTime)

	reduceGroupsName := []string{"basic"}
	if isLLM == "true" {
		reduceGroupsName = []string{"departures-llm"}
	}

	marshalStart := time.Now()
	departureBoardReduced, err := sheriff.Marshal(&sheriff.Options{
		Groups: reduceGroupsName,
	}, departureBoard)
	marshalDuration := time.Since(marshalStart)

	if err != nil {
		c.SendStatus(fiber.StatusInternalServerError)
		return c.JSON(fiber.Map{
			"error": "Sherrif could not reduce departureBoard",
		})
	}

	log.Debug().
		Str("stop", stopIdentifier).
		Int("requested_count", count).
		Time("start_datetime", startDateTime).
		Bool("llm", isLLM == "true").
		Int("departures_before_sort", beforeSortCount).
		Int("departures_after_truncate", afterTruncateCount).
		Int("nil_departure_items", nilDepartureItems).
		Int("destination_fallbacks", destinationFallbacks).
		Int("destination_service_overrides_applied", destinationServiceOverridesApplied).
		Int("transformed_operators", transformedOperators).
		Int("transformed_services", transformedServices).
		Int("reused_operators", reusedOperators).
		Int("reused_services", reusedServices).
		Dur("stop_lookup_duration", stopLookupDuration).
		Dur("departure_lookup_duration", departureLookupDuration).
		Dur("sort_duration", sortDuration).
		Dur("destination_display_duration", destinationDisplayDuration).
		Dur("transform_duration", transformDuration).
		Dur("marshal_duration", marshalDuration).
		Dur("total_duration", time.Since(requestStart)).
		Msg("Stop departures response stats")

	return c.JSON(departureBoardReduced)
}

func resolveDepartureBoardDestinationDisplays(departureBoard []*ctdf.DepartureBoard) (int, int) {
	destinationFallbacks := 0
	destinationServiceOverridesApplied := 0
	destinationStopsByRef := map[string]*ctdf.Stop{}
	serviceStopNameOverridesByRef := map[string]map[string]string{}

	for _, item := range departureBoard {
		if item == nil || item.DestinationDisplay != "" || item.Journey == nil {
			continue
		}

		if len(item.Journey.Path) == 0 {
			item.DestinationDisplay = "See Vehicle"
			destinationFallbacks++
			continue
		}

		destinationFallbacks++
		lastPathItem := item.Journey.Path[len(item.Journey.Path)-1]

		destinationStop := lastPathItem.DestinationStop
		if destinationStop == nil {
			destinationStop = destinationStopsByRef[lastPathItem.DestinationStopRef]
		}
		if destinationStop == nil {
			destinationStop = findDepartureDestinationStop(lastPathItem.DestinationStopRef)
			if destinationStop != nil {
				destinationStopsByRef[lastPathItem.DestinationStopRef] = destinationStop
				destinationStopsByRef[destinationStop.PrimaryIdentifier] = destinationStop
				for _, otherIdentifier := range destinationStop.OtherIdentifiers {
					destinationStopsByRef[otherIdentifier] = destinationStop
				}
			}
		}

		if destinationStop == nil {
			item.DestinationDisplay = "See Vehicle"
			continue
		}

		serviceStopNameOverrides := map[string]string(nil)
		if item.Journey.Service != nil {
			serviceStopNameOverrides = item.Journey.Service.StopNameOverrides
		} else if item.Journey.ServiceRef != "" {
			serviceStopNameOverrides = serviceStopNameOverridesByRef[item.Journey.ServiceRef]
			if serviceStopNameOverrides == nil {
				serviceStopNameOverrides = findDepartureServiceStopNameOverrides(item.Journey.ServiceRef)
				serviceStopNameOverridesByRef[item.Journey.ServiceRef] = serviceStopNameOverrides
			}
		}

		destinationDisplay, overrideApplied := resolveStopDisplayName(destinationStop, serviceStopNameOverrides)
		if overrideApplied {
			destinationServiceOverridesApplied++
		}
		item.DestinationDisplay = destinationDisplay
	}

	return destinationFallbacks, destinationServiceOverridesApplied
}

func findDepartureDestinationStop(stopRef string) *ctdf.Stop {
	if stopRef == "" {
		return nil
	}

	stopsCollection := database.GetCollection("stops")
	var stop *ctdf.Stop
	opts := options.FindOne().SetProjection(bson.D{
		bson.E{Key: "_id", Value: 0},
		bson.E{Key: "primaryidentifier", Value: 1},
		bson.E{Key: "otheridentifiers", Value: 1},
		bson.E{Key: "primaryname", Value: 1},
	})
	stopsCollection.FindOne(context.Background(), bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": stopRef},
			bson.M{"otheridentifiers": stopRef},
		},
	}, opts).Decode(&stop)

	return stop
}

func findDepartureServiceStopNameOverrides(serviceRef string) map[string]string {
	if serviceRef == "" {
		return nil
	}

	servicesCollection := database.GetCollection("services")
	var service *ctdf.Service
	opts := options.FindOne().SetProjection(bson.D{
		bson.E{Key: "_id", Value: 0},
		bson.E{Key: "stopnameoverrides", Value: 1},
	})
	servicesCollection.FindOne(context.Background(), bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": serviceRef},
			bson.M{"otheridentifiers": serviceRef},
		},
	}, opts).Decode(&service)

	if service == nil {
		return nil
	}

	return service.StopNameOverrides
}

func resolveStopDisplayName(stop *ctdf.Stop, stopNameOverrides map[string]string) (string, bool) {
	if stop == nil {
		return "See Vehicle", false
	}

	for _, stopID := range stop.GetAllStopIDs() {
		if stopNameOverrides[stopID] != "" {
			return stopNameOverrides[stopID], stopNameOverrides[stopID] != stop.PrimaryName
		}
	}

	return stop.PrimaryName, false
}

func transformDepartureBoardReferences(departureBoard []*ctdf.DepartureBoard) (int, int, int, int, int) {
	nilDepartureItems := 0
	transformedOperators := 0
	transformedServices := 0
	reusedOperators := 0
	reusedServices := 0
	operatorRefs := map[string]struct{}{}
	serviceRefs := map[string]struct{}{}

	for _, item := range departureBoard {
		if item == nil || item.Journey == nil {
			continue
		}

		if item.Journey.Operator == nil && item.Journey.OperatorRef != "" {
			operatorRefs[item.Journey.OperatorRef] = struct{}{}
		}
		if item.Journey.Service == nil && item.Journey.ServiceRef != "" {
			serviceRefs[item.Journey.ServiceRef] = struct{}{}
		}
	}

	operatorsByID := loadOperatorsByReferences(operatorRefs)
	servicesByID := loadServicesByReferences(serviceRefs)
	transformedOperatorPointers := map[*ctdf.Operator]struct{}{}
	transformedServicePointers := map[*ctdf.Service]struct{}{}

	for _, item := range departureBoard {
		if item == nil || item.Journey == nil {
			nilDepartureItems++
			continue
		}

		operatorKey := item.Journey.OperatorRef
		if operatorKey == "" && item.Journey.Operator != nil {
			operatorKey = item.Journey.Operator.PrimaryIdentifier
		}
		if item.Journey.Operator == nil && operatorKey != "" {
			item.Journey.Operator = operatorsByID[operatorKey]
		}
		if item.Journey.Operator != nil {
			if _, alreadyTransformed := transformedOperatorPointers[item.Journey.Operator]; alreadyTransformed {
				reusedOperators++
			} else {
				transforms.Transform(item.Journey.Operator, 1)
				transformedOperatorPointers[item.Journey.Operator] = struct{}{}
				transformedOperators++
			}
		}

		serviceKey := item.Journey.ServiceRef
		if serviceKey == "" && item.Journey.Service != nil {
			serviceKey = item.Journey.Service.PrimaryIdentifier
		}
		if item.Journey.Service == nil && serviceKey != "" {
			item.Journey.Service = servicesByID[serviceKey]
		}
		if item.Journey.Service != nil {
			if _, alreadyTransformed := transformedServicePointers[item.Journey.Service]; alreadyTransformed {
				reusedServices++
			} else {
				transforms.Transform(item.Journey.Service, 1)
				transformedServicePointers[item.Journey.Service] = struct{}{}
				transformedServices++
			}
		}
	}

	return nilDepartureItems, transformedOperators, transformedServices, reusedOperators, reusedServices
}

func loadOperatorsByReferences(refs map[string]struct{}) map[string]*ctdf.Operator {
	identifiers := departureBoardReferenceIdentifiers(refs)
	operatorsByID := make(map[string]*ctdf.Operator, len(identifiers))
	if len(identifiers) == 0 {
		return operatorsByID
	}

	cursor, err := database.GetCollection("operators").Find(context.Background(), bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": bson.M{"$in": identifiers}},
			bson.M{"otheridentifiers": bson.M{"$in": identifiers}},
		},
	})
	if err != nil {
		log.Error().Err(err).Int("references", len(identifiers)).Msg("Failed to batch load departure board operators")
		return operatorsByID
	}
	defer cursor.Close(context.Background())

	for cursor.Next(context.Background()) {
		var operator ctdf.Operator
		if err := cursor.Decode(&operator); err != nil {
			log.Error().Err(err).Msg("Failed to decode departure board operator")
			continue
		}

		operatorsByID[operator.PrimaryIdentifier] = &operator
		for _, identifier := range operator.OtherIdentifiers {
			operatorsByID[identifier] = &operator
		}
	}

	return operatorsByID
}

func loadServicesByReferences(refs map[string]struct{}) map[string]*ctdf.Service {
	identifiers := departureBoardReferenceIdentifiers(refs)
	servicesByID := make(map[string]*ctdf.Service, len(identifiers))
	if len(identifiers) == 0 {
		return servicesByID
	}

	cursor, err := database.GetCollection("services").Find(context.Background(), bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": bson.M{"$in": identifiers}},
			bson.M{"otheridentifiers": bson.M{"$in": identifiers}},
		},
	})
	if err != nil {
		log.Error().Err(err).Int("references", len(identifiers)).Msg("Failed to batch load departure board services")
		return servicesByID
	}
	defer cursor.Close(context.Background())

	for cursor.Next(context.Background()) {
		var service ctdf.Service
		if err := cursor.Decode(&service); err != nil {
			log.Error().Err(err).Msg("Failed to decode departure board service")
			continue
		}

		servicesByID[service.PrimaryIdentifier] = &service
		for _, identifier := range service.OtherIdentifiers {
			servicesByID[identifier] = &service
		}
	}

	return servicesByID
}

func departureBoardReferenceIdentifiers(refs map[string]struct{}) []string {
	identifiers := make([]string, 0, len(refs))
	for identifier := range refs {
		identifiers = append(identifiers, identifier)
	}
	return identifiers
}

func searchStops(c *fiber.Ctx) error {
	searchTerm := c.Query("name")
	transportType := c.Query("transporttype")
	isLLM := strings.ToLower(c.Query("isllm"))

	if searchTerm == "" {
		c.SendStatus(fiber.StatusBadRequest)
		return c.JSON(fiber.Map{
			"error": "Missing `name` query parameter",
		})
	}

	if elastic_client.Client == nil {
		c.SendStatus(fiber.StatusServiceUnavailable)
		return c.JSON(fiber.Map{
			"error": "Search is currently unavailable",
		})
	}

	queryFilters := []interface{}{
		map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					map[string]interface{}{
						"match_phrase_prefix": map[string]interface{}{
							"PrimaryName.search_as_you_type": searchTerm,
						},
					},
					map[string]interface{}{
						"match_phrase_prefix": map[string]interface{}{
							"OtherIdentifiers.search_as_you_type": searchTerm,
						},
					},
				},
			},
		},
	}

	if transportType != "" {
		queryFilters = append(queryFilters, map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					map[string]interface{}{
						"match": map[string]interface{}{
							"TransportTypes": transportType,
						},
					},
				},
			},
		})
	}

	var queryBytes bytes.Buffer
	searchQuery := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"filter": queryFilters,
			},
		},
		"collapse": map[string]interface{}{
			"field": "PrimaryIdentifier.keyword",
		},
	}

	json.NewEncoder(&queryBytes).Encode(searchQuery)
	res, err := elastic_client.Client.Search(
		elastic_client.Client.Search.WithContext(context.Background()),
		elastic_client.Client.Search.WithIndex("travigo-stops-*"),
		elastic_client.Client.Search.WithBody(&queryBytes),
		elastic_client.Client.Search.WithPretty(),
		elastic_client.Client.Search.WithSize(10),
	)

	if err != nil {
		log.Error().Err(err).Msg("Failed to query index")
		return nil
	}

	responseBytes, _ := io.ReadAll(res.Body)

	var responseStruct struct {
		Took     int  `json:"took"`
		TimedOut bool `json:"timed_out"`
		Hits     struct {
			MaxScore float64 `json:"max_score"`
			Total    struct {
				Value    string `json:"value"`
				Relation string `json:"relation"`
			} `json:"total"`
			Hits []struct {
				Index  string    `json:"_index"`
				Source ctdf.Stop `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	json.Unmarshal(responseBytes, &responseStruct)

	stops := []ctdf.Stop{}
	for _, hit := range responseStruct.Hits.Hits {
		stops = append(stops, hit.Source)
	}

	reduceGroupName := "search"
	if isLLM == "true" {
		reduceGroupName = "search-llm"
	}

	stopsReduced, err := sheriff.Marshal(&sheriff.Options{
		Groups: []string{reduceGroupName},
	}, stops)

	return c.JSON(fiber.Map{
		"stops": stopsReduced,
	})
}
