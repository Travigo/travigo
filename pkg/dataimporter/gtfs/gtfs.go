package gtfs

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/gocarina/gocsv"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/exp/maps"
)

type GTFS struct {
	Agencies      []Agency
	Stops         []Stop
	Routes        []Route
	Trips         []Trip
	StopTimes     []StopTime
	Calendars     []Calendar
	CalendarDates []CalendarDate
	Frequencies   []Frequency
	Shapes        []Shape
}

func ParseZip(path string) (*GTFS, error) {
	// Allow us to ignore those naughty records that have missing columns
	gocsv.SetCSVReader(func(in io.Reader) gocsv.CSVReader {
		r := csv.NewReader(in)
		r.FieldsPerRecord = -1
		return r
	})

	gtfs := &GTFS{}

	fileMap := map[string]interface{}{
		"agency.txt":         &gtfs.Agencies,
		"stops.txt":          &gtfs.Stops,
		"routes.txt":         &gtfs.Routes,
		"trips.txt":          &gtfs.Trips,
		"stop_times.txt":     &gtfs.StopTimes,
		"calendar.txt":       &gtfs.Calendars,
		"calendar_dates.txt": &gtfs.CalendarDates,
		// "frequencies.txt":    &gtfs.Frequencies,
		// "shapes.txt":         &gtfs.Shapes,
	}

	archive, err := zip.OpenReader(path)
	if err != nil {
		panic(err)
	}
	defer archive.Close()

	for _, zipFile := range archive.File {
		file, err := zipFile.Open()
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to open file")
		}
		defer file.Close()

		fileName := zipFile.Name
		if destination, exists := fileMap[fileName]; exists {
			log.Info().Str("file", fileName).Msg("Loading file")

			fileReader, _ := zipFile.Open()
			defer fileReader.Close()

			err = gocsv.Unmarshal(fileReader, destination)
			if err != nil {
				log.Fatal().Str("file", fileName).Err(err).Msg("Failed to parse csv file")
			}
		} else {
			log.Error().Str("file", fileName).Msg("Unknown gtfs file")
		}
	}

	return gtfs, nil
}

func (g *GTFS) ImportIntoMongoAsCTDF(datasetID string) {
	servicesCollection := database.GetCollection("services_gtfs")
	journeysCollection := database.GetCollection("journeys_gtfs")

	log.Info().Msg("Converting & Importing as CTDF into MongoDB")

	// Agencies / Operators
	// TODO this mapping is hardcoding for the 1 UK datset and will need replacing later on to be more generic
	agencyNOCMapping := map[string]string{}
	for _, agency := range g.Agencies {
		if agency.NOC == "" {
			log.Debug().Str("agency", agency.ID).Msg("has no NOC mapping")
			continue
		}
		agencyNOCMapping[agency.ID] = fmt.Sprintf("GB:NOC:%s", agency.NOC)
	}

	// Calendars
	calendarMapping := map[string]*Calendar{}
	calendarDateMapping := map[string][]*CalendarDate{}
	for _, calendar := range g.Calendars {
		calendarMapping[calendar.ServiceID] = &calendar
	}
	for _, calendarDate := range g.CalendarDates {
		calendarDateMapping[calendarDate.ServiceID] = append(calendarDateMapping[calendarDate.ServiceID], &calendarDate)
	}

	// Routes / Services
	routeTypeMapping := map[int]ctdf.TransportType{
		0:   ctdf.TransportTypeTram,
		1:   ctdf.TransportTypeMetro,
		2:   ctdf.TransportTypeRail,
		3:   ctdf.TransportTypeBus,
		4:   ctdf.TransportTypeFerry,
		5:   ctdf.TransportTypeTram,
		6:   ctdf.TransportTypeCableCar,
		7:   ctdf.TransportTypeUnknown, // Furnicular
		11:  ctdf.TransportTypeTram,
		12:  ctdf.TransportTypeUnknown, // Monorail
		200: ctdf.TransportTypeCoach,
	}

	log.Info().Msg("Starting Services")
	ctdfServices := map[string]*ctdf.Service{}
	for _, gtfsRoute := range g.Routes {
		serviceID := fmt.Sprintf("%s-service-%s", datasetID, gtfsRoute.ID)

		serviceName := gtfsRoute.LongName
		if serviceName == "" {
			serviceName = gtfsRoute.ShortName
		}

		operatorRef := agencyNOCMapping[gtfsRoute.AgencyID]
		if operatorRef == "" {
			operatorRef = fmt.Sprintf("%s-operator-%s", datasetID, gtfsRoute.AgencyID)
		}

		ctdfService := &ctdf.Service{
			PrimaryIdentifier: serviceID,
			OtherIdentifiers: map[string]string{
				"GTFS-ID": gtfsRoute.ID,
			},
			CreationDateTime:     time.Now(),
			ModificationDateTime: time.Now(),
			DataSource:           &ctdf.DataSource{},
			ServiceName:          serviceName,
			OperatorRef:          operatorRef,
			Routes:               []ctdf.Route{},
			BrandColour:          gtfsRoute.Colour,
			SecondaryBrandColour: gtfsRoute.TextColour,
			TransportType:        routeTypeMapping[gtfsRoute.Type],
		}

		ctdfServices[gtfsRoute.ID] = ctdfService

		// Insert
		opts := options.Update().SetUpsert(true)
		servicesCollection.UpdateOne(context.Background(), bson.M{"primaryidentifier": serviceID}, bson.M{"$set": ctdfService}, opts)
	}
	log.Info().Msg("Finished Services")

	ctdfJourneys := map[string]*ctdf.Journey{}

	// Journeys
	log.Info().Msg("Starting Journeys")
	for _, trip := range g.Trips {
		journeyID := fmt.Sprintf("%s-journey-%s", datasetID, trip.ID)
		serviceID := fmt.Sprintf("%s-service-%s", datasetID, trip.RouteID)

		availability := &ctdf.Availability{}
		// Calendar availability
		calendar, exists := calendarMapping[trip.ServiceID]
		if exists {
			for _, day := range calendar.GetRunningDays() {
				availability.Match = append(availability.Match, ctdf.AvailabilityRule{
					Type:  ctdf.AvailabilityDayOfWeek,
					Value: day,
				})
			}

			dateRunsFrom, _ := time.Parse("20060102", calendar.Start)
			dateRunsTo, _ := time.Parse("20060102", calendar.End)

			availability.Condition = append(availability.Condition, ctdf.AvailabilityRule{
				Type:  ctdf.AvailabilityDateRange,
				Value: fmt.Sprintf("%s:%s", dateRunsFrom.Format("2006-01-02"), dateRunsTo.Format("2006-01-02")),
			})
		}
		// Calendar dates availability
		for _, calendarDate := range calendarDateMapping[trip.ServiceID] {
			date, _ := time.Parse("20060102", calendarDate.Date)
			rule := ctdf.AvailabilityRule{
				Type:  ctdf.AvailabilityDate,
				Value: date.Format("2006-01-02"),
			}

			if calendarDate.ExceptionType == 1 {
				availability.Match = append(availability.Match, rule)
			} else if calendarDate.ExceptionType == 2 {
				availability.Exclude = append(availability.Exclude, rule)
			}
		}

		// Put it all together again
		ctdfJourneys[trip.ID] = &ctdf.Journey{
			PrimaryIdentifier:    journeyID,
			OtherIdentifiers:     map[string]string{},
			CreationDateTime:     time.Now(),
			ModificationDateTime: time.Now(),
			ServiceRef:           serviceID,
			OperatorRef:          ctdfServices[trip.RouteID].OperatorRef,
			Direction:            trip.DirectionID,
			DepartureTime:        time.Time{},
			DestinationDisplay:   trip.Headsign,
			Availability:         availability,
			Path:                 []*ctdf.JourneyPathItem{},
		}
	}

	// Stop Times
	// Build the actual path of the journeys
	tripStopSequenceMap := map[string]map[int]*StopTime{}
	for _, stopTime := range g.StopTimes {
		if _, exists := tripStopSequenceMap[stopTime.TripID]; !exists {
			tripStopSequenceMap[stopTime.TripID] = map[int]*StopTime{}
		}
		tripStopSequenceMap[stopTime.TripID][stopTime.StopSequence] = &stopTime
	}

	for tripID, tripSequencyMap := range tripStopSequenceMap {
		sequenceIDs := maps.Keys(tripSequencyMap)
		sort.Ints(sequenceIDs)

		for index := 1; index < len(sequenceIDs); index += 1 {
			sequenceID := sequenceIDs[index]
			stopTime := tripSequencyMap[sequenceID]

			previousSequenceID := sequenceIDs[index-1]
			previousStopTime := tripSequencyMap[previousSequenceID]

			originArrivalTime, _ := time.Parse("15:04:05", previousStopTime.ArrivalTime)
			originDeparturelTime, _ := time.Parse("15:04:05", previousStopTime.DepartureTime)
			destinationArrivalTime, _ := time.Parse("15:04:05", stopTime.ArrivalTime)

			journeyPathItem := &ctdf.JourneyPathItem{
				OriginStopRef:          fmt.Sprintf("GB:ATCO:%s", previousStopTime.StopID),
				DestinationStopRef:     fmt.Sprintf("GB:ATCO:%s", stopTime.StopID),
				OriginArrivalTime:      originArrivalTime,
				DestinationArrivalTime: destinationArrivalTime,
				OriginDepartureTime:    originDeparturelTime,
				DestinationDisplay:     stopTime.StopHeadsign,
				OriginActivity:         []ctdf.JourneyPathItemActivity{},
				DestinationActivity:    []ctdf.JourneyPathItemActivity{},
				Track:                  []ctdf.Location{},
			}

			if previousStopTime.DropOffType == "0" || previousStopTime.DropOffType == "" {
				journeyPathItem.OriginActivity = append(journeyPathItem.OriginActivity, ctdf.JourneyPathItemActivitySetdown)
			}
			if previousStopTime.PickupType == "0" || previousStopTime.PickupType == "" {
				journeyPathItem.OriginActivity = append(journeyPathItem.OriginActivity, ctdf.JourneyPathItemActivityPickup)
			}
			if stopTime.DropOffType == "0" || stopTime.DropOffType == "" {
				journeyPathItem.DestinationActivity = append(journeyPathItem.DestinationActivity, ctdf.JourneyPathItemActivitySetdown)
			}
			if stopTime.PickupType == "0" || stopTime.PickupType == "" {
				journeyPathItem.DestinationActivity = append(journeyPathItem.DestinationActivity, ctdf.JourneyPathItemActivityPickup)
			}

			ctdfJourneys[tripID].Path = append(ctdfJourneys[tripID].Path, journeyPathItem)
		}

		// Insert
		opts := options.Update().SetUpsert(true)
		journeysCollection.UpdateOne(context.Background(), bson.M{"primaryidentifier": ctdfJourneys[tripID].PrimaryIdentifier}, bson.M{"$set": ctdfJourneys[tripID]}, opts)

		ctdfJourneys[tripID] = nil
	}
	log.Info().Msg("Finished Journeys")
}
