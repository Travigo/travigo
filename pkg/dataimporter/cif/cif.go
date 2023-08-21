package cif

import (
	"archive/zip"
	"context"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var suffixCheck = regexp.MustCompile(`^[2-9]+$`)
var stopTIPLOCCache = map[string]*ctdf.Stop{}
var daysOfWeek = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
var failedStops []string

type CommonInterfaceFormat struct {
	TrainDefinitionSets []TrainDefinitionSet
	Associations        []Association

	PhysicalStations []PhysicalStation
	StationAliases   []StationAlias

	TIPLOCToCrsMap map[string]string
}

type Association struct {
	TransactionType     string
	BaseUID             string
	AssocUID            string
	AssocStartDate      string
	AssocEndDate        string
	AssocDays           string
	AssocCat            string
	AssocDateInd        string
	AssocLocation       string
	BaseLocationSuffix  string
	AssocLocationSuffix string
	DiagramType         string
	AssociationType     string
	STPIndicator        string
}

func ParseCifBundle(source string) (CommonInterfaceFormat, error) {
	cifBundle := CommonInterfaceFormat{}

	archive, err := zip.OpenReader(source)
	if err != nil {
		log.Fatal().Str("source", source).Err(err).Msg("Could not open zip file")
	}
	defer archive.Close()

	for _, zipFile := range archive.File {

		file, err := zipFile.Open()
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to open file")
		}
		defer file.Close()

		fileExtension := filepath.Ext(zipFile.Name)

		switch fileExtension {
		case ".MCA":
			log.Info().Str("file", zipFile.Name).Msgf("Parsing Full Basic Timetable Detail")
			cifBundle.ParseMCA(file)
			log.Info().
				Int("schedules", len(cifBundle.TrainDefinitionSets)).
				Int("associations", len(cifBundle.Associations)).
				Msgf("Parsed Full Basic Timetable Detail")
		case ".MSN":
			log.Info().Str("file", zipFile.Name).Msgf("Parsing Master Station Names")
			cifBundle.ParseMSN(file)
			log.Info().
				Int("stations", len(cifBundle.PhysicalStations)).
				Int("aliases", len(cifBundle.StationAliases)).
				Msgf("Parsed Master Station Names")
		}
	}

	cifBundle.TIPLOCToCrsMap = map[string]string{}
	for _, station := range cifBundle.PhysicalStations {
		cifBundle.TIPLOCToCrsMap[station.TIPLOCCode] = station.CRSCode
	}

	return cifBundle, nil
}

func (c *CommonInterfaceFormat) ConvertToCTDF() []*ctdf.Journey {
	journeys := map[string]*ctdf.Journey{}

	for _, trainDef := range c.TrainDefinitionSets {
		// Skip buses and ships
		if trainDef.BasicSchedule.TrainCategory == "BS" || trainDef.BasicSchedule.TrainCategory == "SS" {
			continue
		}

		// Skip London Underground (?) records
		if trainDef.BasicScheduleExtraDetails.ATOCCode == "LT" {
			continue
		}
		// TODO Skip Nexus (Tyne & Wear Metro) records
		// The naptan data puts the stops as bus/metro stops with no CRS/TIPLOC
		if trainDef.BasicScheduleExtraDetails.ATOCCode == "TW" {
			continue
		}

		journeyID := fmt.Sprintf("GB:RAIL:%s:%s:%s", trainDef.BasicSchedule.TrainUID, trainDef.BasicSchedule.DateRunsFrom, trainDef.BasicSchedule.STPIndicator)

		// Create whole new journeys
		if trainDef.BasicSchedule.TransactionType == "N" && (trainDef.BasicSchedule.STPIndicator == "P" || trainDef.BasicSchedule.STPIndicator == "N") {
			journeys[journeyID] = c.createJourneyFromTraindef(journeyID, trainDef)
		} else if trainDef.BasicSchedule.TransactionType == "N" && trainDef.BasicSchedule.STPIndicator == "C" {
			// Handle a cancelation
			journey := journeys[journeyID]

			if journey == nil {
				continue
			}

			dateRunsFrom, _ := time.Parse("060102", trainDef.BasicSchedule.DateRunsFrom)
			dateRunsTo, _ := time.Parse("060102", trainDef.BasicSchedule.DateRunsTo)

			journey.Availability.Exclude = append(journey.Availability.Exclude, ctdf.AvailabilityRule{
				Type:  ctdf.AvailabilityDateRange,
				Value: fmt.Sprintf("%s:%s", dateRunsFrom.Format("2006-01-02"), dateRunsTo.Format("2006-01-02")),
			})
		} else if trainDef.BasicSchedule.TransactionType == "N" && trainDef.BasicSchedule.STPIndicator == "O" {
			// Handle an overlay
			// Do this by excluding the date range on the original journey and then creating a new one with the overlay
			originalJourney := journeys[journeyID]

			if originalJourney != nil {
				dateRunsFrom, _ := time.Parse("060102", trainDef.BasicSchedule.DateRunsFrom)
				dateRunsTo, _ := time.Parse("060102", trainDef.BasicSchedule.DateRunsTo)

				originalJourney.Availability.Exclude = append(originalJourney.Availability.Exclude, ctdf.AvailabilityRule{
					Type:        ctdf.AvailabilityDateRange,
					Value:       fmt.Sprintf("%s:%s", dateRunsFrom.Format("2006-01-02"), dateRunsTo.Format("2006-01-02")),
					Description: fmt.Sprintf("Overlay with %s", journeyID),
				})
			}

			journeys[journeyID] = c.createJourneyFromTraindef(journeyID, trainDef)
		} else {
			log.Error().
				Str("transactiontype", trainDef.BasicSchedule.TransactionType).
				Str("stp", trainDef.BasicSchedule.STPIndicator).
				Str("trainuid", trainDef.BasicSchedule.TrainUID).
				Msg("Unhandled transaction/stp combination")
		}
	}

	failedStops = util.RemoveDuplicateStrings(failedStops, []string{})
	log.Error().Interface("tiplocs", failedStops).Msg("Could not find Tiplocs")

	var journeysArray []*ctdf.Journey

	for _, journey := range journeys {
		journeysArray = append(journeysArray, journey)
	}

	return journeysArray
}

func (c *CommonInterfaceFormat) ImportIntoMongoAsCTDF(datasource *ctdf.DataSource) {
	log.Info().Msg("Converting to CTDF")

	journeys := c.ConvertToCTDF()

	log.Info().Msgf(" - %d Journeys", len(journeys))

	// Journeys table
	journeysCollection := database.GetCollection("journeys")

	// Import journeys
	log.Info().Msg("Importing CTDF Journeys into Mongo")
	var operationInsert uint64
	var operationUpdate uint64

	maxBatchSize := int(math.Ceil(float64(len(journeys)) / float64(5*runtime.NumCPU())))
	numBatches := int(math.Ceil(float64(len(journeys)) / float64(maxBatchSize)))

	processingGroup := sync.WaitGroup{}
	processingGroup.Add(numBatches)

	for i := 0; i < numBatches; i++ {
		lower := maxBatchSize * i
		upper := maxBatchSize * (i + 1)

		if upper > len(journeys) {
			upper = len(journeys)
		}

		batchSlice := journeys[lower:upper]

		go func(batch []*ctdf.Journey) {
			var operations []mongo.WriteModel
			var localOperationInsert uint64
			var localOperationUpdate uint64

			for _, journey := range batch {
				var existingCtdfJourney *ctdf.Journey
				journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": journey.PrimaryIdentifier}).Decode(&existingCtdfJourney)

				if existingCtdfJourney == nil {
					journey.CreationDateTime = time.Now()
					journey.ModificationDateTime = time.Now()
					journey.DataSource = datasource

					insertModel := mongo.NewInsertOneModel()

					bsonRep, _ := bson.Marshal(journey)
					insertModel.SetDocument(bsonRep)

					operations = append(operations, insertModel)
					localOperationInsert += 1
				} else if existingCtdfJourney.ModificationDateTime != journey.ModificationDateTime {
					journey.CreationDateTime = existingCtdfJourney.CreationDateTime
					journey.ModificationDateTime = time.Now()
					journey.DataSource = datasource

					updateModel := mongo.NewUpdateOneModel()
					updateModel.SetFilter(bson.M{"primaryidentifier": journey.PrimaryIdentifier})

					bsonRep, _ := bson.Marshal(bson.M{"$set": journey})
					updateModel.SetUpdate(bsonRep)

					operations = append(operations, updateModel)
					localOperationUpdate += 1
				}
			}

			atomic.AddUint64(&operationInsert, localOperationInsert)
			atomic.AddUint64(&operationUpdate, localOperationUpdate)

			if len(operations) > 0 {
				_, err := journeysCollection.BulkWrite(context.TODO(), operations, &options.BulkWriteOptions{})
				if err != nil {
					log.Fatal().Err(err).Msg("Failed to bulk write Journeys")
				}
			}

			processingGroup.Done()
		}(batchSlice)
	}

	processingGroup.Wait()

	log.Info().Msg(" - Written to MongoDB")
	log.Info().Msgf(" - %d inserts", operationInsert)
	log.Info().Msgf(" - %d updates", operationUpdate)
}

func (c *CommonInterfaceFormat) createJourneyFromTraindef(journeyID string, trainDef TrainDefinitionSet) *ctdf.Journey {
	departureTime, _ := time.Parse("1504", trainDef.OriginLocation.PublicDepartureTime)

	// List of passenger stops
	// Start with the origin location
	passengerStops := []BasicLocation{
		{
			Location:               strings.TrimSpace(trainDef.OriginLocation.Location),
			ScheduledArrivalTime:   trainDef.OriginLocation.ScheduledDepartureTime,
			PublicArrivalTime:      trainDef.OriginLocation.PublicDepartureTime,
			ScheduledDepartureTime: trainDef.OriginLocation.ScheduledDepartureTime,
			PublicDepartureTime:    trainDef.OriginLocation.PublicDepartureTime,
			Platform:               trainDef.OriginLocation.Platform,
		},
	}

	// Add all the intermediate stops that are actual passenger stations
	for _, location := range trainDef.IntermediateLocations {
		// No public arrival time? guess its not a real stop
		if location.PublicArrivalTime == "0000" {
			continue
		}

		tiploc := strings.TrimSpace(location.Location)

		// Get rid of the suffix from the tiploc
		if len(tiploc) == 8 && suffixCheck.MatchString(tiploc[7:8]) {
			tiploc = strings.TrimSpace(tiploc[0:7])
		}

		passengerStops = append(passengerStops, BasicLocation{
			Location: tiploc,

			ScheduledDepartureTime: location.ScheduledDepartureTime,
			PublicDepartureTime:    location.PublicDepartureTime,

			ScheduledArrivalTime: location.ScheduledArrivalTime,
			PublicArrivalTime:    location.PublicArrivalTime,

			Platform: location.Platform,
		})
	}

	// Add terminating location to passenger stops
	terminatingTiploc := strings.TrimSpace(trainDef.TerminatingLocation.Location)
	// Get rid of the suffix from the tiploc
	if len(terminatingTiploc) == 8 && suffixCheck.MatchString(terminatingTiploc[7:8]) {
		terminatingTiploc = strings.TrimSpace(terminatingTiploc[0:7])
	}
	passengerStops = append(passengerStops, BasicLocation{
		Location:             strings.TrimSpace(terminatingTiploc),
		ScheduledArrivalTime: trainDef.TerminatingLocation.ScheduledArrivalTime,
		PublicArrivalTime:    trainDef.TerminatingLocation.PublicArrivalTime,
		Platform:             trainDef.TerminatingLocation.Platform,
	})

	// Generate a CTDF path from the passenger stops
	var path []*ctdf.JourneyPathItem

	for i := 1; i < len(passengerStops); i++ {
		originPassengerStop := passengerStops[i-1]
		originTIPLOC := originPassengerStop.Location
		originStop := c.getStopFromTIPLOC(originTIPLOC)
		originArrivalTime, _ := time.Parse("1504", originPassengerStop.PublicArrivalTime)
		originDepartureTime, _ := time.Parse("1504", originPassengerStop.PublicDepartureTime)

		destinationPassengerStop := passengerStops[i]
		destinationTIPLOC := destinationPassengerStop.Location
		destinationStop := c.getStopFromTIPLOC(destinationTIPLOC)
		destinationArrivalTime, _ := time.Parse("1504", destinationPassengerStop.PublicArrivalTime)

		if originStop == nil {
			//log.Error().Str("tiploc", originTIPLOC).Msg("Unknown stop")
			failedStops = append(failedStops, originTIPLOC)
			continue
		}
		if destinationStop == nil {
			//log.Error().Str("tiploc", destinationTIPLOC).Msg("Unknown stop")
			failedStops = append(failedStops, destinationTIPLOC)
			continue
		}

		path = append(path, &ctdf.JourneyPathItem{
			OriginStop:          originStop,
			OriginStopRef:       originStop.PrimaryIdentifier,
			OriginArrivalTime:   originArrivalTime,
			OriginDepartureTime: originDepartureTime,
			OriginPlatform:      strings.TrimSpace(originPassengerStop.Platform),

			DestinationStop:        destinationStop,
			DestinationStopRef:     destinationStop.PrimaryIdentifier,
			DestinationArrivalTime: destinationArrivalTime,
			DestinationPlatform:    strings.TrimSpace(destinationPassengerStop.Platform),
		})
	}

	destinationDisplay := "See Timetable"
	if len(path) > 0 {
		destinationDisplay = path[len(path)-1].DestinationStop.PrimaryName
	}

	// Calculate the base availability for this journey
	availability := &ctdf.Availability{
		Match:          []ctdf.AvailabilityRule{},
		MatchSecondary: []ctdf.AvailabilityRule{},
		Condition:      []ctdf.AvailabilityRule{},
		Exclude:        []ctdf.AvailabilityRule{},
	}
	dateRunsFrom, _ := time.Parse("060102", trainDef.BasicSchedule.DateRunsFrom)
	dateRunsTo, _ := time.Parse("060102", trainDef.BasicSchedule.DateRunsTo)

	for i, ch := range trainDef.BasicSchedule.DaysRun {
		if fmt.Sprintf("%c", ch) == "1" {
			availability.Match = append(availability.Match, ctdf.AvailabilityRule{
				Type:  ctdf.AvailabilityDayOfWeek,
				Value: daysOfWeek[i],
			})
		}
	}

	availability.Condition = append(availability.Condition, ctdf.AvailabilityRule{
		Type:  ctdf.AvailabilityDateRange,
		Value: fmt.Sprintf("%s:%s", dateRunsFrom.Format("2006-01-02"), dateRunsTo.Format("2006-01-02")),
	})

	operatorRef := fmt.Sprintf("GB:TOC:%s", trainDef.BasicScheduleExtraDetails.ATOCCode)

	// Calculate transport type (rail or replacement bus)
	annotations := map[string]interface{}{}
	if trainDef.BasicSchedule.TrainCategory == "BR" {
		annotations["transporttype.rail.replacementbus"] = true
	}

	// Put it all together
	journey := &ctdf.Journey{
		PrimaryIdentifier: journeyID,
		OtherIdentifiers: map[string]string{
			"TrainUID":         trainDef.BasicSchedule.TrainUID,
			"TrainIdentity":    trainDef.BasicSchedule.TrainIdentity,
			"HeadCode":         trainDef.BasicSchedule.Headcode,
			"TrainServiceCode": trainDef.BasicSchedule.TrainServiceCode,
		},
		CreationDateTime:     time.Now(),
		ModificationDateTime: time.Now(),
		DataSource: &ctdf.DataSource{
			OriginalFormat: "CIF",
			Provider:       "GB-NationalRail",
			Dataset:        "timetable",
			Identifier:     "",
		},
		ServiceRef:         operatorRef,
		OperatorRef:        operatorRef,
		DepartureTime:      departureTime,
		DestinationDisplay: destinationDisplay,
		Availability:       availability,
		Path:               path,
		Annotations:        annotations,
	}

	return journey
}

func (c *CommonInterfaceFormat) getStopFromTIPLOC(tiploc string) *ctdf.Stop {
	cacheValue := stopTIPLOCCache[tiploc]

	if cacheValue != nil {
		return cacheValue
	}

	stopCollection := database.GetCollection("stops")
	var stop *ctdf.Stop

	stopCollection.FindOne(context.Background(), bson.M{"otheridentifiers.Tiploc": tiploc}).Decode(&stop)

	// If cant directly find the stop using tiploc then use the MSN map to lookup by CRS
	if stop == nil && c.TIPLOCToCrsMap[tiploc] != "" {
		stopCollection.FindOne(context.Background(), bson.M{"otheridentifiers.Crs": c.TIPLOCToCrsMap[tiploc]}).Decode(&stop)
	}

	stopTIPLOCCache[tiploc] = stop

	return stop
}
