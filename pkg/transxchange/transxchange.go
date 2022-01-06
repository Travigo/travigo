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

	journeyPatternReferences := map[string]map[string]JourneyPattern{} // TODO: all these should be pointers instead

	// There should be so few services (probably just 1) services defined per document that there is no point of batch processing them
	for _, txcService := range doc.Services {
		for _, txcLine := range txcService.Lines {
			// Generate the CTDF Service Record
			operatorRef := operatorLocalMapping[txcService.RegisteredOperatorRef]

			serviceIdentifier := fmt.Sprintf("%s:%s:%s", operatorRef, txcService.ServiceCode, txcLine.ID)

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

				StartDate: txcService.StartDate,
				EndDate:   txcService.EndDate,

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

			// Add JourneyPatterns into reference map
			journeyPatternReferences[serviceIdentifier] = map[string]JourneyPattern{}
			for _, journeyPattern := range txcService.JourneyPatterns {
				journeyPatternReferences[serviceIdentifier][journeyPattern.ID] = journeyPattern
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
	var journeyOperationInsert uint64
	var journeyOperationUpdate uint64

	maxBatchSize := int(len(doc.VehicleJourneys) / runtime.NumCPU())
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
				operatorRef := operatorLocalMapping[txcJourney.OperatorRef]
				serviceRef := fmt.Sprintf("%s:%s:%s", operatorRef, txcJourney.ServiceRef, txcJourney.LineRef)

				journeyPattern := journeyPatternReferences[serviceRef][txcJourney.JourneyPatternRef]
				journeyPatternSection := journeyPatternSectionReferences[journeyPattern.JourneyPatternSectionRefs]

				route := routeReferences[journeyPattern.RouteRef]
				routeSection := routeSectionReferences[route.RouteSectionRef]

				departureTime, _ := time.Parse("15:04:05", txcJourney.DepartureTime)

				ctdfJourney := ctdf.Journey{
					PrimaryIdentifier: fmt.Sprintf("%s:%s", operatorRef, txcJourney.PrivateCode),
					OtherIdentifiers: map[string]string{
						"PrivateCode": txcJourney.PrivateCode,
						"JourneyCode": txcJourney.VehicleJourneyCode,
					},

					CreationDateTime:     txcJourney.CreationDateTime,
					ModificationDateTime: txcJourney.ModificationDateTime,

					DataSource: datasource,

					ServiceRef:         serviceRef,
					OperatorRef:        operatorRef,
					Direction:          txcJourney.Direction,
					DeperatureTime:     departureTime,
					DestinationDisplay: journeyPattern.DestinationDisplay,

					// Availability *Availability

					Path: []ctdf.JourneyPathItem{},
				}

				timeCursor, _ := time.Parse("15:04:05", txcJourney.DepartureTime)

				for _, vehicleJourneyTimingLink := range txcJourney.VehicleJourneyTimingLinks {
					journeyPatternTimingLink, _ := journeyPatternSection.GetTimingLink(vehicleJourneyTimingLink.JourneyPatternTimingLinkRef)

					routeLink, _ := routeSection.GetRouteLink(journeyPatternTimingLink.RouteLinkRef)

					// Calculate timings
					originArivalTime := timeCursor

					// TODO: wait time calculation here
					originDepartureTime := timeCursor // TODO: also need to handle wait times

					travelTime, _ := iso8601.ParseISO8601(vehicleJourneyTimingLink.RunTime)
					destinationArivalTime := travelTime.Shift(originDepartureTime)
					timeCursor = destinationArivalTime

					pathItem := ctdf.JourneyPathItem{
						OriginStopRef:      fmt.Sprintf(ctdf.StopIDFormat, journeyPatternTimingLink.From.StopPointRef),
						DestinationStopRef: fmt.Sprintf(ctdf.StopIDFormat, journeyPatternTimingLink.To.StopPointRef),

						Distance: routeLink.Distance,

						OriginArivalTime:    originArivalTime,
						OriginDepartureTime: originDepartureTime,

						DestinationArivalTime: destinationArivalTime,
					}

					ctdfJourney.Path = append(ctdfJourney.Path, pathItem)
				}

				bsonRep, _ := bson.Marshal(ctdfJourney)

				var existingCtdfJourney *ctdf.Journey
				journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": ctdfJourney.PrimaryIdentifier}).Decode(&existingCtdfJourney)

				if existingCtdfJourney == nil {
					insertModel := mongo.NewInsertOneModel()
					insertModel.SetDocument(bsonRep)

					stopOperations = append(stopOperations, insertModel)
					localOperationInsert += 1
				} else if existingCtdfJourney.ModificationDateTime != ctdfJourney.ModificationDateTime {
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
