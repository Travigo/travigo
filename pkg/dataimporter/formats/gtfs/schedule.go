package gtfs

import (
	"archive/zip"
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/gocarina/gocsv"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/transforms"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/exp/maps"
)

type Schedule struct {
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

func ParseScheduleZip(path string) (*Schedule, error) {
	// Allow us to ignore those naughty records that have missing columns
	gocsv.SetCSVReader(func(in io.Reader) gocsv.CSVReader {
		r := csv.NewReader(in)
		r.FieldsPerRecord = -1
		return r
	})

	gtfs := &Schedule{}

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

func (g *Schedule) Import(datasetID string, datasource *ctdf.DataSource) {
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

	log.Info().Int("length", len(g.Routes)).Msg("Starting Services")
	servicesQueue := NewDatabaseBatchProcessingQueue("services", 1*time.Second, 10*time.Second, 500)
	servicesQueue.Process()

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
			DataSource:           datasource,
			ServiceName:          serviceName,
			OperatorRef:          operatorRef,
			Routes:               []ctdf.Route{},
			BrandColour:          gtfsRoute.Colour,
			SecondaryBrandColour: gtfsRoute.TextColour,
			TransportType:        routeTypeMapping[gtfsRoute.Type],
		}

		transforms.Transform(ctdfService, 1, "gb-bods-gtfs")

		ctdfServices[gtfsRoute.ID] = ctdfService

		// Insert
		bsonRep, _ := bson.Marshal(bson.M{"$set": ctdfService})
		updateModel := mongo.NewUpdateOneModel()
		updateModel.SetFilter(bson.M{"primaryidentifier": serviceID})
		updateModel.SetUpdate(bsonRep)
		updateModel.SetUpsert(true)

		servicesQueue.Add(updateModel)
	}
	log.Info().Msg("Finished Services")
	servicesQueue.Wait()

	ctdfJourneys := map[string]*ctdf.Journey{}

	// Journeys
	journeysQueue := NewDatabaseBatchProcessingQueue("journeys", 1*time.Second, 1*time.Minute, 1000)
	journeysQueue.Process()

	log.Info().Int("length", len(g.Trips)).Msg("Starting Journeys")
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
			PrimaryIdentifier: journeyID,
			OtherIdentifiers: map[string]string{
				"GTFS-TripID":  trip.ID,
				"GTFS-RouteID": trip.RouteID,
			},
			CreationDateTime:     time.Now(),
			ModificationDateTime: time.Now(),
			DataSource:           datasource,
			ServiceRef:           serviceID,
			OperatorRef:          ctdfServices[trip.RouteID].OperatorRef,
			// Direction:            trip.DirectionID,
			DestinationDisplay: trip.Headsign,
			Availability:       availability,
			Path:               []*ctdf.JourneyPathItem{},
		}

		if trip.BlockID != "" {
			ctdfJourneys[trip.ID].OtherIdentifiers["BlockNumber"] = trip.BlockID
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

			if previousStopTime.DropOffType == 0 {
				journeyPathItem.OriginActivity = append(journeyPathItem.OriginActivity, ctdf.JourneyPathItemActivitySetdown)
			}
			if previousStopTime.PickupType == 0 {
				journeyPathItem.OriginActivity = append(journeyPathItem.OriginActivity, ctdf.JourneyPathItemActivityPickup)
			}
			if stopTime.DropOffType == 0 {
				journeyPathItem.DestinationActivity = append(journeyPathItem.DestinationActivity, ctdf.JourneyPathItemActivitySetdown)
			}
			if stopTime.PickupType == 0 {
				journeyPathItem.DestinationActivity = append(journeyPathItem.DestinationActivity, ctdf.JourneyPathItemActivityPickup)
			}

			ctdfJourneys[tripID].Path = append(ctdfJourneys[tripID].Path, journeyPathItem)

			if index == 1 {
				ctdfJourneys[tripID].DepartureTime = originDeparturelTime
			}
		}

		// TODO fix transforms here
		// transforms.Transform(ctdfJourneys[tripID], 1, "gb-bods-gtfs")
		if util.ContainsString([]string{
			"GB:NOC:LDLR", "GB:NOC:LULD", "GB:NOC:TRAM", "gb-bods-gtfs-operator-OPTEMP454",
			"GB:NOC:ABLO", "gb-bods-gtfs-operator-OP12046", "GB:NOC:ALNO", "GB:NOC:ALSO", "gb-bods-gtfs-operator-OPTEMP450", "gb-bods-gtfs-operator-OP11684",
			"GB:NOC:ELBG", "gb-bods-gtfs-operator-OPTEMP456", "gb-bods-gtfs-operator-OP3039", "GB:NOC:LSOV", "GB:NOC:LUTD", "gb-bods-gtfs-operator-OP2974",
			"GB:NOC:MTLN", "GB:NOC:SULV",
		}, ctdfJourneys[tripID].OperatorRef) || (ctdfJourneys[tripID].OperatorRef == "GB:NOC:UNIB" && util.ContainsString([]string{
			"gb-bods-gtfs-service-14023", "gb-bods-gtfs-service-13950", "gb-bods-gtfs-service-14053", "gb-bods-gtfs-service-13966", "gb-bods-gtfs-service-13968", "gb-bods-gtfs-service-82178",
		}, ctdfJourneys[tripID].ServiceRef)) {
			ctdfJourneys[tripID].OperatorRef = "GB:NOC:TFLO"
		}

		// Insert
		bsonRep, _ := bson.Marshal(bson.M{"$set": ctdfJourneys[tripID]})
		updateModel := mongo.NewUpdateOneModel()
		updateModel.SetFilter(bson.M{"primaryidentifier": ctdfJourneys[tripID].PrimaryIdentifier})
		updateModel.SetUpdate(bsonRep)
		updateModel.SetUpsert(true)

		journeysQueue.Add(updateModel)

		ctdfJourneys[tripID] = nil
	}
	log.Info().Msg("Finished Journeys")

	journeysQueue.Wait()
}

/////// THE DEAD ZONE ////////
// r := csv.NewReader(fileReader)
// 				r.FieldsPerRecord = -1
// 				if _, err := r.Read(); err != nil { //read header
// 					log.Fatal().Err(err).Msg("csv header read")
// 				}
// 				for {
// 					rec, err := r.Read()
// 					if err != nil {
// 						if err == io.EOF {
// 							break
// 						}
// 						log.Fatal().Err(err).Msg("csv row read")

// 					}

// 					stopSequence, _ := strconv.Atoi(rec[4])

// 					var stopHeadsign string
// 					if len(rec) >= 6 {
// 						stopHeadsign = rec[5]
// 					}

// 					var pickupType int
// 					if len(rec) >= 7 {
// 						pickupType, _ = strconv.Atoi(rec[6])
// 					}
// 					var dropOffType int
// 					if len(rec) >= 8 {
// 						dropOffType, _ = strconv.Atoi(rec[7])
// 					}

// 					gtfs.StopTimes = append(gtfs.StopTimes, &StopTime{
// 						// trip_id,arrival_time,departure_time,stop_id,stop_sequence,stop_headsign,pickup_type,drop_off_type,shape_dist_traveled,timepoint,stop_direction_name
// 						TripID:        rec[0],
// 						ArrivalTime:   rec[1],
// 						DepartureTime: rec[2],
// 						StopID:        rec[3],
// 						StopSequence:  stopSequence,
// 						StopHeadsign:  stopHeadsign,
// 						PickupType:    pickupType,
// 						DropOffType:   dropOffType,
// 					})
// 				}
