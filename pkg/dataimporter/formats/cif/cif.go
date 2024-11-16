package cif

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
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
	TrainDefinitionSets []*TrainDefinitionSet
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

func (c *CommonInterfaceFormat) ParseFile(reader io.Reader) error {
	// TODO this uses a load of ram :(
	body, err := io.ReadAll(reader)
	if err != nil {
		return err
	}

	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return err
	}

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
			c.ParseMCA(file)
			log.Info().
				Int("schedules", len(c.TrainDefinitionSets)).
				Int("associations", len(c.Associations)).
				Msgf("Parsed Full Basic Timetable Detail")
		case ".MSN":
			log.Info().Str("file", zipFile.Name).Msgf("Parsing Master Station Names")
			c.ParseMSN(file)
			log.Info().
				Int("stations", len(c.PhysicalStations)).
				Int("aliases", len(c.StationAliases)).
				Msgf("Parsed Master Station Names")
		}
	}

	c.TIPLOCToCrsMap = map[string]string{}
	for _, station := range c.PhysicalStations {
		c.TIPLOCToCrsMap[station.TIPLOCCode] = station.CRSCode
	}

	return nil
}

func (c *CommonInterfaceFormat) ConvertToCTDF() []*ctdf.Journey {
	journeys := map[string]*ctdf.Journey{}
	journeysTrainUIDOnly := map[string][]*ctdf.Journey{}

	for _, trainDef := range c.TrainDefinitionSets {
		basicJourneyID := fmt.Sprintf("gb-rail-%s:%s", trainDef.BasicSchedule.TrainUID, trainDef.BasicSchedule.DateRunsFrom)
		journeyID := fmt.Sprintf("gb-rail-%s:%s:%s", trainDef.BasicSchedule.TrainUID, trainDef.BasicSchedule.DateRunsFrom, trainDef.BasicSchedule.STPIndicator)

		// Create whole new journeys
		if trainDef.BasicSchedule.TransactionType == "N" && (trainDef.BasicSchedule.STPIndicator == "P" || trainDef.BasicSchedule.STPIndicator == "N") {
			// Only care about relevant passenger trains
			if !IsValidPassengerJourney(trainDef.BasicSchedule.TrainCategory, trainDef.BasicScheduleExtraDetails.ATOCCode) {
				continue
			}
			journeys[basicJourneyID] = c.CreateJourneyFromTraindef(journeyID, trainDef)

			journeysTrainUIDOnly[trainDef.BasicSchedule.TrainUID] = append(journeysTrainUIDOnly[trainDef.BasicSchedule.TrainUID], journeys[basicJourneyID])
		} else if trainDef.BasicSchedule.TransactionType == "N" && trainDef.BasicSchedule.STPIndicator == "C" {
			// Handle a cancelation

			for _, journey := range journeysTrainUIDOnly[trainDef.BasicSchedule.TrainUID] {
				dateRunsFrom, _ := time.Parse("060102", trainDef.BasicSchedule.DateRunsFrom)
				dateRunsTo, _ := time.Parse("060102", trainDef.BasicSchedule.DateRunsTo)

				journey.Availability.Exclude = append(journey.Availability.Exclude, ctdf.AvailabilityRule{
					Type:  ctdf.AvailabilityDateRange,
					Value: fmt.Sprintf("%s:%s", dateRunsFrom.Format("2006-01-02"), dateRunsTo.Format("2006-01-02")),
				})
			}
		} else if trainDef.BasicSchedule.TransactionType == "N" && trainDef.BasicSchedule.STPIndicator == "O" {
			// Only care about relevant passenger trains
			if !IsValidPassengerJourney(trainDef.BasicSchedule.TrainCategory, trainDef.BasicScheduleExtraDetails.ATOCCode) {
				continue
			}

			// Handle an overlay
			// Do this by excluding the date range on the original journey and then creating a new one with the overlay
			for _, journey := range journeysTrainUIDOnly[trainDef.BasicSchedule.TrainUID] {
				dateRunsFrom, _ := time.Parse("060102", trainDef.BasicSchedule.DateRunsFrom)
				dateRunsTo, _ := time.Parse("060102", trainDef.BasicSchedule.DateRunsTo)

				journey.Availability.Exclude = append(journey.Availability.Exclude, ctdf.AvailabilityRule{
					Type:        ctdf.AvailabilityDateRange,
					Value:       fmt.Sprintf("%s:%s", dateRunsFrom.Format("2006-01-02"), dateRunsTo.Format("2006-01-02")),
					Description: fmt.Sprintf("Overlay with %s", journeyID),
				})
			}

			journeys[journeyID] = c.CreateJourneyFromTraindef(journeyID, trainDef)
			journeysTrainUIDOnly[trainDef.BasicSchedule.TrainUID] = append(journeysTrainUIDOnly[trainDef.BasicSchedule.TrainUID], journeys[journeyID])
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
		// Skip zero path journeys
		if len(journey.Path) == 0 {
			continue
		}
		journeysArray = append(journeysArray, journey)
		// pretty.Println(journey.Availability)
	}

	return journeysArray
}

func (c *CommonInterfaceFormat) Import(dataset datasets.DataSet, datasource *ctdf.DataSourceReference) error {
	if !dataset.SupportedObjects.Journeys || !dataset.SupportedObjects.Services {
		return errors.New("This format requires services & journeys to be enabled")
	}
	log.Info().Msg("Converting to CTDF")

	journeys := c.ConvertToCTDF()

	log.Info().Msgf(" - %d Journeys", len(journeys))

	// Journeys table
	journeysCollection := database.GetCollection("journeys")

	// Import journeys
	log.Info().Msg("Importing CTDF Journeys into Mongo")
	var operationInsert uint64

	maxBatchSize := 200
	numBatches := int(math.Ceil(float64(len(journeys)) / float64(maxBatchSize)))

	for i := 0; i < numBatches; i++ {
		lower := maxBatchSize * i
		upper := maxBatchSize * (i + 1)

		if upper > len(journeys) {
			upper = len(journeys)
		}

		batchSlice := journeys[lower:upper]

		var operations []mongo.WriteModel

		for _, journey := range batchSlice {
			journey.CreationDateTime = time.Now()
			journey.ModificationDateTime = time.Now()
			journey.DataSource = datasource

			insertModel := mongo.NewInsertOneModel()

			bsonRep, _ := bson.Marshal(journey)
			insertModel.SetDocument(bsonRep)

			operations = append(operations, insertModel)
			operationInsert += 1
		}

		if len(operations) > 0 {
			_, err := journeysCollection.BulkWrite(context.Background(), operations, &options.BulkWriteOptions{})
			if err != nil {
				log.Fatal().Err(err).Msg("Failed to bulk write Journeys")
			}
		}
	}

	log.Info().Msg(" - Written to MongoDB")
	log.Info().Msgf(" - %d inserts", operationInsert)

	return nil
}

func (c *CommonInterfaceFormat) CreateJourneyFromTraindef(journeyID string, trainDef *TrainDefinitionSet) *ctdf.Journey {
	departureTime, _ := time.Parse("1504", util.TrimString(trainDef.OriginLocation.PublicDepartureTime, 4))

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
			Activity:               trainDef.OriginLocation.Activity,
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
			Activity: location.Activity,
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
		Activity:             trainDef.TerminatingLocation.Activity,
	})

	// Generate a CTDF path from the passenger stops
	var path []*ctdf.JourneyPathItem

	for i := 1; i < len(passengerStops); i++ {
		originPassengerStop := passengerStops[i-1]
		originTIPLOC := originPassengerStop.Location
		originStop := c.getStopFromTIPLOC(originTIPLOC)
		originArrivalTime, _ := time.Parse("1504", util.TrimString(originPassengerStop.PublicArrivalTime, 4))
		originDepartureTime, _ := time.Parse("1504", util.TrimString(originPassengerStop.PublicDepartureTime, 4))

		destinationPassengerStop := passengerStops[i]
		destinationTIPLOC := destinationPassengerStop.Location
		destinationStop := c.getStopFromTIPLOC(destinationTIPLOC)
		destinationArrivalTime, _ := time.Parse("1504", util.TrimString(destinationPassengerStop.PublicArrivalTime, 4))

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

			OriginActivity:      convertStopActivity(originPassengerStop.Activity),
			DestinationActivity: convertStopActivity(destinationPassengerStop.Activity),
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

	operatorRef := fmt.Sprintf(ctdf.OperatorTOCFormat, trainDef.BasicScheduleExtraDetails.ATOCCode)

	////// Detailed rail information //////
	detailedRailInformation := ctdf.JourneyDetailedRail{
		AirConditioning: strings.Contains(trainDef.BasicSchedule.OperatingCharacteristics, "R"),

		ReservationRequired:     strings.Contains(trainDef.BasicSchedule.Reservations, "A"),
		ReservationBikeRequired: strings.Contains(trainDef.BasicSchedule.Reservations, "E"),
		ReservationRecommended:  strings.Contains(trainDef.BasicSchedule.Reservations, "R"),
	}

	// Seating type
	if strings.TrimSpace(trainDef.BasicSchedule.SeatingClass) == "" || trainDef.BasicSchedule.SeatingClass == "B" {
		detailedRailInformation.Seating = []ctdf.JourneyDetailedRailSeating{ctdf.JourneyDetailedRailSeatingFirst, ctdf.JourneyDetailedRailSeatingStandard}
	} else if trainDef.BasicSchedule.SeatingClass == "S" {
		detailedRailInformation.Seating = []ctdf.JourneyDetailedRailSeating{ctdf.JourneyDetailedRailSeatingStandard}
	} else {
		detailedRailInformation.Seating = []ctdf.JourneyDetailedRailSeating{ctdf.JourneyDetailedRailSeatingUnknown}
	}

	// Sleepers
	if trainDef.BasicSchedule.Sleepers == "B" {
		detailedRailInformation.SleeperAvailable = true
		detailedRailInformation.Sleepers = []ctdf.JourneyDetailedRailSeating{ctdf.JourneyDetailedRailSeatingFirst, ctdf.JourneyDetailedRailSeatingStandard}
	} else if trainDef.BasicSchedule.Sleepers == "F" {
		detailedRailInformation.SleeperAvailable = true
		detailedRailInformation.Sleepers = []ctdf.JourneyDetailedRailSeating{ctdf.JourneyDetailedRailSeatingFirst}
	} else if trainDef.BasicSchedule.Sleepers == "S" {
		detailedRailInformation.SleeperAvailable = true
		detailedRailInformation.Sleepers = []ctdf.JourneyDetailedRailSeating{ctdf.JourneyDetailedRailSeatingStandard}
	} else {
		detailedRailInformation.SleeperAvailable = false
	}

	// Catering
	cateringDescriptions := []string{}
	if strings.Contains(trainDef.BasicSchedule.CateringCode, "C") {
		cateringDescriptions = append(cateringDescriptions, "Buffet service")
		detailedRailInformation.CateringAvailable = true
	} else if strings.Contains(trainDef.BasicSchedule.CateringCode, "F") {
		cateringDescriptions = append(cateringDescriptions, "Restaurant Car available for First Class passengers")
		detailedRailInformation.CateringAvailable = true
	} else if strings.Contains(trainDef.BasicSchedule.CateringCode, "H") {
		cateringDescriptions = append(cateringDescriptions, "Hot food available")
		detailedRailInformation.CateringAvailable = true
	} else if strings.Contains(trainDef.BasicSchedule.CateringCode, "M") {
		cateringDescriptions = append(cateringDescriptions, "Meal included for First Class passengers")
		detailedRailInformation.CateringAvailable = true
	} else if strings.Contains(trainDef.BasicSchedule.CateringCode, "P") {
		cateringDescriptions = append(cateringDescriptions, "Wheelchair only reservations")
		detailedRailInformation.CateringAvailable = true
	} else if strings.Contains(trainDef.BasicSchedule.CateringCode, "R") {
		cateringDescriptions = append(cateringDescriptions, "Restaurant")
		detailedRailInformation.CateringAvailable = true
	} else if strings.Contains(trainDef.BasicSchedule.CateringCode, "T") {
		cateringDescriptions = append(cateringDescriptions, "Trolley service")
		detailedRailInformation.CateringAvailable = true
	}

	detailedRailInformation.CateringDescription = strings.Join(cateringDescriptions, ". ")

	// Speed
	speedMPH, _ := strconv.Atoi(trainDef.BasicSchedule.Speed)
	detailedRailInformation.SpeedKMH = int(float64(speedMPH) * 1.60934)

	// Train class
	trainClass := "unknown"
	if trainDef.BasicSchedule.PowerType == "DMU" || trainDef.BasicSchedule.PowerType == "DEM" || trainDef.BasicSchedule.PowerType == "D  " {
		detailedRailInformation.PowerType = "Diesel"

		switch strings.TrimSpace(trainDef.BasicSchedule.TimingLoad) {
		case "69":
			trainClass = "172"
		case "A":
			trainClass = "141_144"
		case "E":
			trainClass = "158_168_170_175"
		case "N":
			trainClass = "165"
		case "S":
			trainClass = "150_153_155_156"
		case "T":
			trainClass = "166"
		case "V":
			trainClass = "220_221"
		case "X":
			trainClass = "159"
		case "":
			trainClass = strings.TrimSpace(trainDef.BasicSchedule.PowerType)
		default:
			trainClass = strings.TrimSpace(trainDef.BasicSchedule.TimingLoad)
		}
	} else if trainDef.BasicSchedule.PowerType == "EMU" || trainDef.BasicSchedule.PowerType == "E  " {
		detailedRailInformation.PowerType = "Electric"

		switch strings.TrimSpace(trainDef.BasicSchedule.TimingLoad) {
		case "AT":
			trainClass = "AT" // this shouldnt ever exist i believe
		case "E":
			trainClass = "458"
		case "0":
			trainClass = "380"
		case "506":
			trainClass = "350/1"
		case "":
			trainClass = strings.TrimSpace(trainDef.BasicSchedule.PowerType)
		default:
			trainClass = strings.TrimSpace(trainDef.BasicSchedule.TimingLoad)
		}
	} else if trainDef.BasicSchedule.PowerType == "HST" {
		trainClass = "HST"
		detailedRailInformation.PowerType = "Diesel"
	}

	detailedRailInformation.VehicleType = fmt.Sprintf("gb-railclass-%s", trainClass)

	// Rail replacement bus
	if trainDef.BasicSchedule.TrainCategory == "BR" {
		detailedRailInformation.ReplacementBus = true
		detailedRailInformation.VehicleType = "gb-railclass-REPLACEMENTBUS"
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
		ServiceRef:           operatorRef,
		OperatorRef:          operatorRef,
		DepartureTime:        departureTime,
		DepartureTimezone:    "Europe/London",
		DestinationDisplay:   destinationDisplay,
		Availability:         availability,
		Path:                 path,

		DetailedRailInformation: &detailedRailInformation,
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

	stopCollection.FindOne(context.Background(), bson.M{"otheridentifiers": fmt.Sprintf("gb-tiploc-%s", tiploc)}).Decode(&stop)

	// If cant directly find the stop using tiploc then use the MSN map to lookup by CRS
	if stop == nil && c.TIPLOCToCrsMap[tiploc] != "" {
		stopCollection.FindOne(context.Background(), bson.M{"otheridentifiers": fmt.Sprintf("gb-crs-%s", c.TIPLOCToCrsMap[tiploc])}).Decode(&stop)
	}

	stopTIPLOCCache[tiploc] = stop

	return stop
}

func convertStopActivity(activity string) []ctdf.JourneyPathItemActivity {
	activityList := []ctdf.JourneyPathItemActivity{}
	if strings.TrimSpace(activity) == "TB" {
		activityList = []ctdf.JourneyPathItemActivity{
			ctdf.JourneyPathItemActivityPickup,
		}
	} else if strings.TrimSpace(activity) == "TF" {
		activityList = []ctdf.JourneyPathItemActivity{
			ctdf.JourneyPathItemActivitySetdown,
		}
	} else if strings.TrimSpace(activity) == "T" {
		activityList = []ctdf.JourneyPathItemActivity{
			ctdf.JourneyPathItemActivityPickup,
			ctdf.JourneyPathItemActivitySetdown,
		}
	} else if strings.TrimSpace(activity) == "D" {
		activityList = []ctdf.JourneyPathItemActivity{
			ctdf.JourneyPathItemActivitySetdown,
		}
	}

	return activityList
}
