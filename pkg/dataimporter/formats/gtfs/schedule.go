package gtfs

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gocarina/gocsv"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
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

func (gtfs *Schedule) ParseFile(reader io.Reader) error {
	// Allow us to ignore those naughty records that have missing columns
	gocsv.SetCSVReader(func(in io.Reader) gocsv.CSVReader {
		r := csv.NewReader(in)
		r.FieldsPerRecord = -1
		return r
	})

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

		fileName := zipFile.Name
		if destination, exists := fileMap[fileName]; exists {
			log.Info().Str("file", fileName).Msg("Loading file")

			fileReader, _ := zipFile.Open()
			defer fileReader.Close()

			err = gocsv.Unmarshal(fileReader, destination)
			if err != nil {
				log.Error().Str("file", fileName).Err(err).Msg("Failed to parse csv file")
				return err
			}
		} else {
			log.Error().Str("file", fileName).Msg("Unknown gtfs file")
		}
	}

	return nil
}

func (g *Schedule) Import(dataset datasets.DataSet, datasource *ctdf.DataSource) error {
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

	log.Info().Int("length", len(g.Agencies)).Msg("Starting Operators")
	agenciesQueue := NewDatabaseBatchProcessingQueue("operators", 1*time.Second, 10*time.Second, 500)

	if dataset.SupportedObjects.Operators {
		agenciesQueue.Process()
	}
	agenciesMap := map[string]*Agency{}
	for _, gtfsAgency := range g.Agencies {
		agenciesMap[gtfsAgency.ID] = &gtfsAgency
		operatorID := fmt.Sprintf("%s-operator-%s", dataset.Identifier, gtfsAgency.ID)
		ctdfOperator := &ctdf.Operator{
			PrimaryIdentifier:    operatorID,
			CreationDateTime:     time.Now(),
			ModificationDateTime: time.Now(),
			DataSource:           datasource,
			PrimaryName:          gtfsAgency.Name,
			Website:              gtfsAgency.URL,
		}

		if dataset.SupportedObjects.Operators {
			// Insert
			bsonRep, _ := bson.Marshal(bson.M{"$set": ctdfOperator})
			updateModel := mongo.NewUpdateOneModel()
			updateModel.SetFilter(bson.M{"primaryidentifier": operatorID})
			updateModel.SetUpdate(bsonRep)
			updateModel.SetUpsert(true)
			agenciesQueue.Add(updateModel)
		}
	}
	log.Info().Msg("Finished Operators")
	if dataset.SupportedObjects.Operators {
		agenciesQueue.Wait()
	}

	// Stops
	log.Info().Int("length", len(g.Stops)).Msg("Starting Stops")
	stopsQueue := NewDatabaseBatchProcessingQueue("stops", 1*time.Second, 10*time.Second, 500)

	if dataset.SupportedObjects.Stops {
		stopsQueue.Process()
	}
	for _, gtfsStop := range g.Stops {
		stopID := fmt.Sprintf("%s-stop-%s", dataset.Identifier, gtfsStop.ID)
		ctdfStop := &ctdf.Stop{
			PrimaryIdentifier: stopID,
			OtherIdentifiers: map[string]string{
				"GTFS-ID": gtfsStop.ID,
			},
			CreationDateTime:     time.Now(),
			ModificationDateTime: time.Now(),
			DataSource:           datasource,
			PrimaryName:          gtfsStop.Name,
			OtherNames:           map[string]string{},
			Location: &ctdf.Location{
				Type:        "Point",
				Coordinates: []float64{gtfsStop.Longitude, gtfsStop.Latitude},
			},
			Active: true,
		}

		if dataset.SupportedObjects.Stops {
			// Insert
			bsonRep, _ := bson.Marshal(bson.M{"$set": ctdfStop})
			updateModel := mongo.NewUpdateOneModel()
			updateModel.SetFilter(bson.M{"primaryidentifier": stopID})
			updateModel.SetUpdate(bsonRep)
			updateModel.SetUpsert(true)
			stopsQueue.Add(updateModel)
		}
	}
	log.Info().Msg("Finished Stops")
	if dataset.SupportedObjects.Stops {
		stopsQueue.Wait()
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

	if dataset.SupportedObjects.Services {
		servicesQueue.Process()
	}

	ctdfServices := map[string]*ctdf.Service{}
	routeMap := map[string]Route{}
	for _, gtfsRoute := range g.Routes {
		routeMap[gtfsRoute.ID] = gtfsRoute
		serviceID := fmt.Sprintf("%s-service-%s", dataset.Identifier, gtfsRoute.ID)

		serviceName := gtfsRoute.ShortName
		if serviceName == "" {
			serviceName = gtfsRoute.LongName
		}

		operatorRef := agencyNOCMapping[gtfsRoute.AgencyID]
		if operatorRef == "" {
			operatorRef = fmt.Sprintf("%s-operator-%s", dataset.Identifier, gtfsRoute.AgencyID)
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

		transforms.Transform(ctdfService, 1, "gb-dft-bods-gtfs-schedule")

		ctdfServices[gtfsRoute.ID] = ctdfService

		if dataset.SupportedObjects.Services {
			// Insert
			bsonRep, _ := bson.Marshal(bson.M{"$set": ctdfService})
			updateModel := mongo.NewUpdateOneModel()
			updateModel.SetFilter(bson.M{"primaryidentifier": serviceID})
			updateModel.SetUpdate(bsonRep)
			updateModel.SetUpsert(true)
			servicesQueue.Add(updateModel)
		}
	}
	log.Info().Msg("Finished Services")
	if dataset.SupportedObjects.Services {
		servicesQueue.Wait()
	}

	ctdfJourneys := map[string]*ctdf.Journey{}

	// Journeys
	journeysQueue := NewDatabaseBatchProcessingQueue("journeys", 1*time.Second, 1*time.Minute, 1000)
	if dataset.SupportedObjects.Journeys {
		journeysQueue.Process()
	}

	log.Info().Int("length", len(g.Trips)).Msg("Starting Journeys")
	for _, trip := range g.Trips {
		journeyID := fmt.Sprintf("%s-journey-%s", dataset.Identifier, trip.ID)
		serviceID := fmt.Sprintf("%s-service-%s", dataset.Identifier, trip.RouteID)

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
			DepartureTimezone:  agenciesMap[routeMap[trip.RouteID].AgencyID].Timezone,
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

			originArrivalTime, err := time.Parse("15:04:05", fixTimestamp(previousStopTime.ArrivalTime))
			if err != nil {
				log.Error().Err(err).Msg("Failed to parse previousStopTime.ArrivalTime")
			}
			originDeparturelTime, err := time.Parse("15:04:05", fixTimestamp(previousStopTime.DepartureTime))
			if err != nil {
				log.Error().Err(err).Msg("Failed to parse previousStopTime.DepartureTime")
			}
			destinationArrivalTime, err := time.Parse("15:04:05", fixTimestamp(stopTime.ArrivalTime))
			if err != nil {
				log.Error().Err(err).Msg("Failed to parse stopTime.ArrivalTime")
			}

			var originStopRef string
			var destinationStopRef string

			// TODO no hardocded nonsense!!
			if dataset.Identifier == "gb-dft-bods-gtfs-schedule" {
				originStopRef = fmt.Sprintf("GB:ATCO:%s", previousStopTime.StopID)
				destinationStopRef = fmt.Sprintf("GB:ATCO:%s", stopTime.StopID)
			} else {
				originStopRef = fmt.Sprintf("%s-stop-%s", dataset.Identifier, previousStopTime.StopID)
				destinationStopRef = fmt.Sprintf("%s-stop-%s", dataset.Identifier, stopTime.StopID)
			}

			journeyPathItem := &ctdf.JourneyPathItem{
				OriginStopRef:          originStopRef,
				DestinationStopRef:     destinationStopRef,
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
		// transforms.Transform(ctdfJourneys[tripID], 1, "gb-dft-bods-gtfs-schedule")
		if util.ContainsString([]string{
			"GB:NOC:LDLR", "GB:NOC:LULD", "GB:NOC:TRAM", "gb-dft-bods-gtfs-schedule-operator-OPTEMP454",
			"GB:NOC:ABLO", "gb-dft-bods-gtfs-schedule-operator-OP12046", "GB:NOC:ALNO", "GB:NOC:ALSO", "gb-dft-bods-gtfs-schedule-operator-OPTEMP450", "gb-dft-bods-gtfs-schedule-operator-OP11684",
			"GB:NOC:ELBG", "gb-dft-bods-gtfs-schedule-operator-OPTEMP456", "gb-dft-bods-gtfs-schedule-operator-OP3039", "GB:NOC:LSOV", "GB:NOC:LUTD", "gb-dft-bods-gtfs-schedule-operator-OP2974",
			"GB:NOC:MTLN", "GB:NOC:SULV",
		}, ctdfJourneys[tripID].OperatorRef) || (ctdfJourneys[tripID].OperatorRef == "GB:NOC:UNIB" && util.ContainsString([]string{
			"gb-dft-bods-gtfs-schedule-service-14023", "gb-dft-bods-gtfs-schedule-service-13950", "gb-dft-bods-gtfs-schedule-service-14053", "gb-dft-bods-gtfs-schedule-service-13966", "gb-dft-bods-gtfs-schedule-service-13968", "gb-dft-bods-gtfs-schedule-service-82178",
		}, ctdfJourneys[tripID].ServiceRef)) {
			ctdfJourneys[tripID].OperatorRef = "GB:NOC:TFLO"
		}

		// Insert
		if dataset.SupportedObjects.Journeys {
			bsonRep, _ := bson.Marshal(bson.M{"$set": ctdfJourneys[tripID]})
			updateModel := mongo.NewUpdateOneModel()
			updateModel.SetFilter(bson.M{"primaryidentifier": ctdfJourneys[tripID].PrimaryIdentifier})
			updateModel.SetUpdate(bsonRep)
			updateModel.SetUpsert(true)

			journeysQueue.Add(updateModel)
		}

		ctdfJourneys[tripID] = nil
	}
	log.Info().Msg("Finished Journeys")

	if dataset.SupportedObjects.Journeys {
		journeysQueue.Wait()
	}

	return nil
}

func fixTimestamp(timestamp string) string {
	splitTimestamp := strings.Split(timestamp, ":")

	if len(splitTimestamp) != 3 {
		return timestamp
	}

	hour, err := strconv.Atoi(splitTimestamp[0])
	if err != nil {
		return timestamp
	}

	if hour >= 24 {
		splitTimestamp[0] = fmt.Sprintf("%d", hour%24)

		return strings.Join(splitTimestamp, ":")
	} else {
		return timestamp
	}
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
