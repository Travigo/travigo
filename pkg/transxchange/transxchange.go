package transxchange

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/jinzhu/copier"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"

	iso8601 "github.com/senseyeio/duration"
)

type TransXChange struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`

	Operators              []*Operator
	Routes                 []*Route
	Services               []*Service
	JourneyPatternSections []*JourneyPatternSection
	RouteSections          []*RouteSection
	VehicleJourneys        []*VehicleJourney
	ServicedOrganisations  []*ServicedOrganisation

	SchemaVersion string `xml:",attr"`
}

func (n *TransXChange) Validate() error {
	if n.CreationDateTime == "" {
		return errors.New("CreationDateTime must be set")
	}
	if n.ModificationDateTime == "" {
		return errors.New("ModificationDateTime must be set")
	}
	if n.SchemaVersion != "2.4" {
		return errors.New("SchemaVersion must be 2.4")
	}

	return nil
}

func (doc *TransXChange) ImportIntoMongoAsCTDF(datasource *ctdf.DataSource) {
	datasource.OriginalFormat = "transxchange"
	datasource.Identifier = doc.ModificationDateTime

	servicesCollection := database.GetCollection("services")
	journeysCollection := database.GetCollection("journeys")

	//TODO: Doesnt really make sense for the TransXChange package to be managing CTDF tables and indexes
	_, err := servicesCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "primaryidentifier", Value: bsonx.Int32(1)}},
		},
	}, options.CreateIndexes())
	if err != nil {
		panic(err)
	}

	_, err = journeysCollection.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "primaryidentifier", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "serviceref", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "path.originstopref", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "path.destinationstopref", Value: bsonx.Int32(1)}},
		},
	}, options.CreateIndexes())
	if err != nil {
		panic(err)
	}

	// Map the local operator references to globally unique operator codes based on NOC
	operatorLocalMapping := map[string]string{}

	for _, operator := range doc.Operators {
		operatorLocalMapping[operator.ID] = fmt.Sprintf(ctdf.OperatorIDFormat, operator.NationalOperatorCode)
	}

	// Create reference map for JourneyPatternSections
	journeyPatternSectionReferences := map[string]*JourneyPatternSection{} // TODO: all these should be pointers instead
	for _, journeyPatternSection := range doc.JourneyPatternSections {
		journeyPatternSectionReferences[journeyPatternSection.ID] = journeyPatternSection
	}

	// Create reference map for Routes
	routeReferences := map[string]*Route{} // TODO: all these should be pointers instead
	for _, route := range doc.Routes {
		routeReferences[route.ID] = route
	}

	// Create reference map for Routes
	routeSectionReferences := map[string]*RouteSection{} // TODO: all these should be pointers instead
	for _, routeSection := range doc.RouteSections {
		routeSectionReferences[routeSection.ID] = routeSection
	}

	// Get CTDF services from TransXChange Services & Lines
	log.Info().Msg("Converting & Importing CTDF Services into Mongo")
	serviceOperations := []mongo.WriteModel{}
	var serviceOperationInsert uint64
	var serviceOperationUpdate uint64

	journeyPatternReferences := map[string]map[string]*JourneyPattern{} // TODO: all these should be pointers instead
	servicesReferences := map[string]*Service{}

	ignoredServices := map[string]bool{}

	// There should be so few services (probably just 1) services defined per document that there is no point of batch processing them
	for _, txcService := range doc.Services {
		for _, txcLine := range txcService.Lines {
			// Generate the CTDF Service Record
			operatorRef := operatorLocalMapping[txcService.RegisteredOperatorRef]

			serviceIdentifier := fmt.Sprintf("%s:%s:%s", operatorRef, txcService.ServiceCode, txcLine.ID)
			localServiceIdentifier := fmt.Sprintf("%s:%s", txcService.ServiceCode, txcLine.ID)

			servicesReferences[localServiceIdentifier] = txcService

			ctdfService := ctdf.Service{
				PrimaryIdentifier: serviceIdentifier,
				OtherIdentifiers: map[string]string{
					"ServiceCode": txcService.ServiceCode,
					"LineID":      txcLine.ID,
				},

				DataSource: datasource,

				ServiceName:          txcLine.LineName,
				CreationDateTime:     txcService.CreationDateTime,
				ModificationDateTime: txcService.ModificationDateTime,

				OperatorRef: operatorRef,

				InboundDescription: &ctdf.ServiceDescription{
					Origin:      txcLine.InboundOrigin,
					Destination: txcLine.InboundDestination,
					Description: txcLine.InboundDescription,
				},
				OutboundDescription: &ctdf.ServiceDescription{
					Origin:      txcLine.OutboundOrigin,
					Destination: txcLine.OutboundDestination,
					Description: txcLine.OutboundDescription,
				},
			}

			// Check if Service end date is before today and skip over it if that is true
			// We get a lot of duplicate documents included in BODS with expired data so this should ignore them
			if txcService.OperatingPeriod.EndDate != "" {
				endDate, err := time.Parse(ctdf.YearMonthDayFormat, txcService.OperatingPeriod.EndDate)

				if err == nil && endDate.Before(time.Now()) {
					ignoredServices[localServiceIdentifier] = true
					continue
				}
			}

			// Add JourneyPatterns into reference map
			journeyPatternReferences[localServiceIdentifier] = map[string]*JourneyPattern{}
			for _, journeyPattern := range txcService.JourneyPatterns {
				journeyPatternReferences[localServiceIdentifier][journeyPattern.ID] = journeyPattern
			}

			// Check if we want to add this service to the list of MongoDB operations
			bsonRep, _ := bson.Marshal(ctdfService)

			var existingCtdfService *ctdf.Service
			servicesCollection.FindOne(context.Background(), bson.M{"primaryidentifier": ctdfService.PrimaryIdentifier}).Decode(&existingCtdfService)

			if existingCtdfService == nil {
				insertModel := mongo.NewInsertOneModel()
				insertModel.SetDocument(bsonRep)

				serviceOperations = append(serviceOperations, insertModel)
				serviceOperationInsert += 1
			} else if existingCtdfService.ModificationDateTime != ctdfService.ModificationDateTime {
				updateModel := mongo.NewReplaceOneModel()
				updateModel.SetFilter(bson.M{"primaryidentifier": ctdfService.PrimaryIdentifier})
				updateModel.SetReplacement(bsonRep)

				serviceOperations = append(serviceOperations, updateModel)
				serviceOperationUpdate += 1
			}
		}
	}

	if len(serviceOperations) > 0 {
		_, err = servicesCollection.BulkWrite(context.TODO(), serviceOperations, &options.BulkWriteOptions{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write Services")
		}
	}

	log.Info().Msg(" - Written to MongoDB")
	log.Info().Msgf(" - %d inserts", serviceOperationInsert)
	log.Info().Msgf(" - %d updates", serviceOperationUpdate)

	// Get CTDF Journeys from TransXChange VehicleJourneys
	log.Info().Msg("Converting & Importing CTDF Journeys into Mongo")

	// A little cheat for handling frequent services by just duplicating the VehicleJourney record for each frequent run
	for _, txcJourney := range doc.VehicleJourneys {
		if txcJourney.Frequency != nil && txcJourney.Frequency.Interval != nil {
			departureTime, _ := time.Parse("15:04:05", txcJourney.DepartureTime)
			endTime, _ := time.Parse("15:04:05", txcJourney.Frequency.EndTime)
			interval, _ := iso8601.ParseISO8601(txcJourney.Frequency.Interval.ScheduledFrequency)

			for newDepartureTime := interval.Shift(departureTime); newDepartureTime.Sub(endTime) <= 0; newDepartureTime = interval.Shift(newDepartureTime) {
				var copiedJourney VehicleJourney
				err := copier.CopyWithOption(&copiedJourney, *txcJourney, copier.Option{IgnoreEmpty: true, DeepCopy: true})

				if err != nil {
					log.Error().Err(err).Msgf("Failed to copy VehicleJourney %s", txcJourney.VehicleJourneyCode)
					continue
				}

				copiedJourney.DepartureTime = newDepartureTime.Format("15:04:05")
				copiedJourney.VehicleJourneyCode = fmt.Sprintf("%s-%s", copiedJourney.VehicleJourneyCode, copiedJourney.DepartureTime)

				doc.VehicleJourneys = append(doc.VehicleJourneys, &copiedJourney)
			}
		}
	}

	var journeyOperationInsert uint64
	var journeyOperationUpdate uint64

	maxBatchSize := int(math.Ceil(float64(len(doc.VehicleJourneys)) / float64(runtime.NumCPU())))
	numBatches := int(math.Ceil(float64(len(doc.VehicleJourneys)) / float64(maxBatchSize)))

	processingGroup := sync.WaitGroup{}
	processingGroup.Add(numBatches)

	for i := 0; i < numBatches; i++ {
		lower := maxBatchSize * i
		upper := maxBatchSize * (i + 1)

		if upper > len(doc.VehicleJourneys) {
			upper = len(doc.VehicleJourneys)
		}

		batchSlice := doc.VehicleJourneys[lower:upper]

		go func(vehicleJourneys []*VehicleJourney) {
			stopOperations := []mongo.WriteModel{}
			var localOperationInsert uint64
			var localOperationUpdate uint64

			for _, txcJourney := range vehicleJourneys {
				serviceRef := fmt.Sprintf("%s:%s", txcJourney.ServiceRef, txcJourney.LineRef)
				service := servicesReferences[serviceRef]

				if service == nil {
					log.Error().Msgf("Failed to find referenced service in vehicle journey %s", txcJourney.VehicleJourneyCode) // TODO: maybe not a fail condition?
					continue
				}

				// If this service is in the ignore list (eg. expired serviced) then just silently skip over it
				if ignoredServices[serviceRef] {
					continue
				}

				var txcJourneyOperatorRef string
				if txcJourney.OperatorRef != "" {
					txcJourneyOperatorRef = txcJourney.OperatorRef
				} else if service.RegisteredOperatorRef != "" {
					txcJourneyOperatorRef = service.RegisteredOperatorRef
				} else {
					log.Error().Msgf("Failed to find referenced operator in vehicle journey %s", txcJourney.VehicleJourneyCode) // TODO: maybe not a fail condition?
					continue
				}

				operatorRef := operatorLocalMapping[txcJourneyOperatorRef] // NOT ALWAYS THERE, could be in SERVICE DEFINITION

				journeyPattern := journeyPatternReferences[serviceRef][txcJourney.JourneyPatternRef]
				if journeyPattern == nil {
					log.Error().Msgf("Failed to find referenced journeyPattern %s in vehicle journey %s", txcJourney.JourneyPatternRef, txcJourney.VehicleJourneyCode)
					continue
				}

				journeyPatternSection := journeyPatternSectionReferences[journeyPattern.JourneyPatternSectionRefs]
				if journeyPatternSection == nil {
					log.Error().Msgf("Failed to find referenced journeyPatternSection %s for journeyPattern %s in vehicle journey %s", journeyPattern.JourneyPatternSectionRefs, txcJourney.JourneyPatternRef, txcJourney.VehicleJourneyCode)
					continue
				}

				route := routeReferences[journeyPattern.RouteRef]
				if route == nil {
					log.Error().Msgf("Failed to find referenced route %s in vehicle journey %s", journeyPattern.RouteRef, txcJourney.VehicleJourneyCode)
					continue
				}

				// A Route can have many RouteSectionRefs
				// Create an array of references to all the RouteSections for iterating over later
				var routeSections []*RouteSection

				for _, routeSectionRef := range route.RouteSectionRef {
					routeSection := routeSectionReferences[routeSectionRef]
					if routeSection == nil {
						log.Error().Msgf("Failed to find referenced routeSection %s for route %s in vehicle journey %s", route.RouteSectionRef, journeyPattern.RouteRef, txcJourney.VehicleJourneyCode)
						continue
					}
					routeSections = append(routeSections, routeSection)
				}

				if len(routeSections) == 0 {
					log.Error().Msgf("Failed to find any referenced routeSections for route %s in vehicle journey %s", journeyPattern.RouteRef, txcJourney.VehicleJourneyCode)
					continue
				}

				departureTime, _ := time.Parse("15:04:05", txcJourney.DepartureTime)

				// Calculate availability from OperatingProfiles
				var availability *ctdf.Availability

				if service.OperatingProfile.XMLValue != "" {
					serviceAvailability, err := service.OperatingProfile.ToCTDF(doc.ServicedOrganisations)
					if err != nil {
						log.Error().Err(err).Msgf("Error parsing availability for vehicle journey %s", txcJourney.VehicleJourneyCode)
					} else {
						availability = serviceAvailability
					}
				}

				if journeyPattern.OperatingProfile.XMLValue != "" {
					journeyPatternAvailability, err := journeyPattern.OperatingProfile.ToCTDF(doc.ServicedOrganisations)
					if err != nil {
						log.Error().Err(err).Msgf("Error parsing availability for vehicle journey %s", txcJourney.VehicleJourneyCode)
					} else {
						availability = journeyPatternAvailability
					}
				}

				if txcJourney.OperatingProfile.XMLValue != "" {
					journeyAvailability, err := txcJourney.OperatingProfile.ToCTDF(doc.ServicedOrganisations)
					if err != nil {
						log.Error().Err(err).Msgf("Error parsing availability for vehicle journey %s", txcJourney.VehicleJourneyCode)
					} else {
						availability = journeyAvailability
					}
				}

				//Append to Availability Condition based on OperatingPeriod
				// Add if either StartDate or EndDate exists (it can be open ended)
				// If availability doesnt already exist then dont bother as we don't care for this journey at the moment
				if availability != nil && !(service.OperatingPeriod.StartDate == "" && service.OperatingPeriod.EndDate == "") {
					availability.Condition = append(availability.Condition, ctdf.AvailabilityRule{
						Type:  ctdf.AvailabilityDateRange,
						Value: fmt.Sprintf("%s:%s", service.OperatingPeriod.StartDate, service.OperatingPeriod.EndDate),
					})
				}

				if availability == nil || (len(availability.Match) == 0 && len(availability.MatchSecondary) == 0) {
					log.Error().Msgf("Vehicle journey %s has a nil availability", txcJourney.VehicleJourneyCode)
				}

				// Create CTDF Journey record
				ctdfJourney := ctdf.Journey{
					PrimaryIdentifier: fmt.Sprintf("%s:%s:%s", operatorRef, serviceRef, txcJourney.VehicleJourneyCode),
					OtherIdentifiers: map[string]string{
						"PrivateCode": txcJourney.PrivateCode,
						"JourneyCode": txcJourney.VehicleJourneyCode,
					},

					CreationDateTime:     txcJourney.CreationDateTime,
					ModificationDateTime: txcJourney.ModificationDateTime,

					DataSource: datasource,

					ServiceRef:         fmt.Sprintf("%s:%s", operatorRef, serviceRef),
					OperatorRef:        operatorRef,
					Direction:          txcJourney.Direction,
					DepartureTime:      departureTime,
					DestinationDisplay: journeyPattern.DestinationDisplay,

					Availability: availability,

					Path: []ctdf.JourneyPathItem{},
				}

				timeCursor, _ := time.Parse("15:04:05", txcJourney.DepartureTime)

				// Create CTDF Journey path based on TXC VehicleJourney referenced JourneyPatternSection
				for _, journeyPatternTimingLink := range journeyPatternSection.JourneyPatternTimingLinks {
					vehicleJourneyTimingLink := txcJourney.GetVehicleJourneyTimingLinkByJourneyPatternTimingLinkRef(journeyPatternTimingLink.ID)

					// Search for the RouteLink in the many RouteSections assigned with the Route
					var routeLink *RouteLink
					for _, routeSection := range routeSections {
						checkRouteLink, _ := routeSection.GetRouteLink(journeyPatternTimingLink.RouteLinkRef)

						if checkRouteLink != nil {
							routeLink = checkRouteLink
							break
						}
					}

					if routeLink == nil {
						log.Error().Msgf("Failed to find referenced routeLink %s for JPTL %s in vehicle journey %s", journeyPatternTimingLink.RouteLinkRef, journeyPatternTimingLink.ID, txcJourney.VehicleJourneyCode)
						continue
					}

					runTime := journeyPatternTimingLink.RunTime

					if vehicleJourneyTimingLink != nil && vehicleJourneyTimingLink.RunTime != "" {
						runTime = vehicleJourneyTimingLink.RunTime
					}

					// Calculate timings
					originArivalTime := timeCursor

					// Add on any wait times
					var originWaitTime iso8601.Duration

					if journeyPatternTimingLink.From.WaitTime != "" {
						originWaitTime, _ = iso8601.ParseISO8601(journeyPatternTimingLink.From.WaitTime)
					}
					if vehicleJourneyTimingLink != nil && vehicleJourneyTimingLink.From.WaitTime != "" {
						originWaitTime, _ = iso8601.ParseISO8601(vehicleJourneyTimingLink.From.WaitTime)
					}

					timeCursor = originWaitTime.Shift(timeCursor)

					originDepartureTime := timeCursor

					travelTime, _ := iso8601.ParseISO8601(runTime)
					destinationArivalTime := travelTime.Shift(originDepartureTime)
					timeCursor = destinationArivalTime

					// Calculate the destination display at this stop
					destinationDisplay := journeyPattern.DestinationDisplay
					if journeyPatternTimingLink.From.DynamicDestinationDisplay != "" {
						destinationDisplay = journeyPatternTimingLink.From.DynamicDestinationDisplay
					}
					if vehicleJourneyTimingLink != nil && vehicleJourneyTimingLink.From.DynamicDestinationDisplay != "" {
						destinationDisplay = vehicleJourneyTimingLink.From.DynamicDestinationDisplay
					}

					// Get the activities at this stop (eg. pickup, setdown, both)
					var originActivity []ctdf.JourneyPathItemActivity
					var destinationActivity []ctdf.JourneyPathItemActivity

					txcFromActivity := "pickUpAndSetDown"
					txcToActivity := "pickUpAndSetDown"

					if journeyPatternTimingLink.From.Activity != "" {
						txcFromActivity = journeyPatternTimingLink.From.Activity
					}
					if vehicleJourneyTimingLink != nil && vehicleJourneyTimingLink.From.Activity != "" {
						txcFromActivity = vehicleJourneyTimingLink.From.Activity
					}
					if journeyPatternTimingLink.To.Activity != "" {
						txcToActivity = journeyPatternTimingLink.To.Activity
					}
					if vehicleJourneyTimingLink != nil && vehicleJourneyTimingLink.To.Activity != "" {
						txcToActivity = vehicleJourneyTimingLink.To.Activity
					}

					if txcFromActivity == "pickUpAndSetDown" {
						originActivity = []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPickup, ctdf.JourneyPathItemActivitySetdown}
					}
					if txcFromActivity == "pickUp" {
						originActivity = []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPickup}
					}
					if txcFromActivity == "setDown" {
						originActivity = []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivitySetdown}
					}
					if txcFromActivity == "pass" {
						originActivity = []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPass}
					}

					if txcToActivity == "pickUpAndSetDown" {
						destinationActivity = []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPickup, ctdf.JourneyPathItemActivitySetdown}
					}
					if txcToActivity == "pickUp" {
						destinationActivity = []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPickup}
					}
					if txcToActivity == "setDown" {
						destinationActivity = []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivitySetdown}
					}
					if txcToActivity == "pass" {
						destinationActivity = []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPass}
					}

					pathItem := ctdf.JourneyPathItem{
						OriginStopRef:      fmt.Sprintf(ctdf.StopIDFormat, journeyPatternTimingLink.From.StopPointRef),
						DestinationStopRef: fmt.Sprintf(ctdf.StopIDFormat, journeyPatternTimingLink.To.StopPointRef),

						Distance: routeLink.Distance,

						OriginArivalTime:    originArivalTime,
						OriginDepartureTime: originDepartureTime,

						DestinationArivalTime: destinationArivalTime,

						DestinationDisplay: destinationDisplay,

						OriginActivity:      originActivity,
						DestinationActivity: destinationActivity,
					}

					ctdfJourney.Path = append(ctdfJourney.Path, pathItem)
				}

				if len(ctdfJourney.Path) == 0 {
					log.Error().Msgf("Journey %s has a nil path", ctdfJourney.PrimaryIdentifier) //TODO: not an error condition?
				}

				bsonRep, _ := bson.Marshal(ctdfJourney)

				var existingCtdfJourney *ctdf.Journey
				journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": ctdfJourney.PrimaryIdentifier}).Decode(&existingCtdfJourney)

				if existingCtdfJourney == nil {
					insertModel := mongo.NewInsertOneModel()
					insertModel.SetDocument(bsonRep)

					stopOperations = append(stopOperations, insertModel)
					localOperationInsert += 1
				} else if existingCtdfJourney.ModificationDateTime != ctdfJourney.ModificationDateTime { // should be > not !=
					updateModel := mongo.NewReplaceOneModel()
					updateModel.SetFilter(bson.M{"primaryidentifier": ctdfJourney.PrimaryIdentifier})
					updateModel.SetReplacement(bsonRep)

					stopOperations = append(stopOperations, updateModel)
					localOperationUpdate += 1
				}
			}

			atomic.AddUint64(&journeyOperationInsert, localOperationInsert)
			atomic.AddUint64(&journeyOperationUpdate, localOperationUpdate)

			if len(stopOperations) > 0 {
				_, err = journeysCollection.BulkWrite(context.TODO(), stopOperations, &options.BulkWriteOptions{})
				if err != nil {
					log.Fatal().Err(err).Msg("Failed to bulk write Journeys")
				}
			}

			processingGroup.Done()
		}(batchSlice)
	}

	processingGroup.Wait()

	log.Info().Msg(" - Written to MongoDB")
	log.Info().Msgf(" - %d inserts", journeyOperationInsert)
	log.Info().Msgf(" - %d updates", journeyOperationUpdate)

	log.Info().Msgf("Successfully imported into MongoDB")
}
