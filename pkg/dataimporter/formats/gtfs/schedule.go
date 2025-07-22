package gtfs

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/go-csv"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/transforms"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/exp/maps"
)

type Schedule struct {
	fileMap map[string]string

	Agencies []Agency

	calendarMapping     map[string]*Calendar
	calendarDateMapping map[string][]*CalendarDate

	ctdfServices map[string]*ctdf.Service
	routeMap     map[string]Route

	ctdfJourneys map[string]*ctdf.Journey

	tripStopSequenceMap map[string]map[int]*StopTime

	Shapes       []Shape
	Translations []Translation
}

func (gtfs *Schedule) ParseFile(reader io.Reader) error {
	gtfs.fileMap = map[string]string{}

	// TODO HACK as we end up with 2 copies of the file
	tmpZipFile, err := os.CreateTemp(os.TempDir(), "travigo-data-importer-gtfs-zip-")
	if err != nil {
		log.Fatal().Err(err).Msg("Cannot create temporary file")
	}
	io.Copy(tmpZipFile, reader)

	archive, err := zip.OpenReader(tmpZipFile.Name())
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
		fileReader, _ := zipFile.Open()
		defer fileReader.Close()

		// copy each file to a temporary location so it can be parsed later outside of the zip reader
		tmpFile, err := os.CreateTemp(os.TempDir(), "travigo-data-importer-gtfs-file-")
		defer tmpFile.Close()
		if err != nil {
			log.Fatal().Err(err).Msg("Cannot create temporary file")
		}
		io.Copy(tmpFile, fileReader)
		log.Info().Str("file", fileName).Str("tmp", tmpFile.Name()).Msg("Extracting file")
		gtfs.fileMap[fileName] = tmpFile.Name()
	}

	os.Remove(tmpZipFile.Name())

	return nil
}

func importObject[T interface{}](g *Schedule, fileName string, tableName string, toProcess bool, parser func(T) (any, string)) error {
	log.Info().Str("table", tableName).Msg("Processing Table")
	processingQueue := NewDatabaseBatchProcessingQueue(tableName, 1*time.Second, 10*time.Second, 2000)
	if toProcess {
		processingQueue.Process()
	}

	file, _ := os.Open(g.fileMap[fileName])
	defer file.Close()
	decoder := csv.NewDecoder(file)

	line, err := decoder.ReadLine()
	if err != nil {
		return err
	}
	if _, err = decoder.DecodeHeader(line); err != nil {
		return err
	}

	for {
		line, err = decoder.ReadLine()
		if err != nil {
			return err
		}
		if line == "" {
			break
		}

		var v T
		if err = decoder.DecodeRecord(&v, line); err != nil {
			return err
		}
		parsedObject, parsedObjectIdentifier := parser(v)

		if toProcess && parsedObject != nil && parsedObjectIdentifier != "" {
			// Insert
			bsonRep, _ := bson.Marshal(bson.M{"$set": parsedObject})
			updateModel := mongo.NewUpdateOneModel()
			updateModel.SetFilter(bson.M{"primaryidentifier": parsedObjectIdentifier})
			updateModel.SetUpdate(bsonRep)
			updateModel.SetUpsert(true)
			processingQueue.Add(updateModel)
		}
	}

	if toProcess {
		processingQueue.Wait()
	}

	log.Info().Str("table", tableName).Msg("Finished Table")

	os.Remove(g.fileMap[fileName])

	return nil
}

func (g *Schedule) Import(dataset datasets.DataSet, datasource *ctdf.DataSourceReference) error {
	log.Info().Msg("Converting & Importing as CTDF into MongoDB")

	//// Translations ////
	importObject[Translation](g, "translations.txt", "translations", false, func(t Translation) (any, string) {
		g.Translations = append(g.Translations, t)

		return nil, ""
	})

	//// Agencies / Operators ////
	importObject[Agency](g, "agency.txt", "operators", dataset.SupportedObjects.Operators, func(a Agency) (any, string) {
		operatorID := fmt.Sprintf("%s-operator-%s", dataset.Identifier, a.ID)
		ctdfOperator := &ctdf.Operator{
			PrimaryIdentifier:    operatorID,
			CreationDateTime:     time.Now(),
			ModificationDateTime: time.Now(),
			DataSource:           datasource,
			PrimaryName:          g.GetTranslation("agency", "agency_name", "en", a.Name),
			Website:              a.URL,
		}

		g.Agencies = append(g.Agencies, a)

		return ctdfOperator, operatorID
	})

	// TODO this mapping is hardcoding for the 1 UK datset and will need replacing later on to be more generic
	agencyNOCMapping := map[string]string{}
	agenciesMap := map[string]*Agency{}
	for _, agency := range g.Agencies {
		agenciesMap[agency.ID] = &agency

		if agency.NOC == "" {
			log.Debug().Str("agency", agency.ID).Msg("has no NOC mapping")
			continue
		}
		agencyNOCMapping[agency.ID] = fmt.Sprintf("gb-noc-%s", agency.NOC)
	}

	//// Stops ////
	importObject[Stop](g, "stops.txt", "stops_raw", dataset.SupportedObjects.Stops, func(s Stop) (any, string) {
		timezone := s.Timezone

		if timezone == "" {
			timezone = g.Agencies[0].Timezone
		}

		stopID := fmt.Sprintf("%s-stop-%s", dataset.Identifier, s.ID)
		ctdfStop := &ctdf.Stop{
			PrimaryIdentifier:    stopID,
			OtherIdentifiers:     []string{stopID},
			CreationDateTime:     time.Now(),
			ModificationDateTime: time.Now(),
			DataSource:           datasource,
			PrimaryName:          g.GetTranslation("stops", "stop_name", "en", s.Name),
			Location: &ctdf.Location{
				Type:        "Point",
				Coordinates: []float64{s.Longitude, s.Latitude},
			},
			Active:   true,
			Timezone: timezone,
		}

		return ctdfStop, stopID
	})

	//// Calendars ////
	g.calendarMapping = map[string]*Calendar{}
	g.calendarDateMapping = map[string][]*CalendarDate{}
	importObject[Calendar](g, "calendar.txt", "calendars", false, func(c Calendar) (any, string) {
		g.calendarMapping[c.ServiceID] = &c

		return nil, ""
	})
	importObject[CalendarDate](g, "calendar_dates.txt", "calendar_dates", false, func(c CalendarDate) (any, string) {
		g.calendarDateMapping[c.ServiceID] = append(g.calendarDateMapping[c.ServiceID], &c)

		return nil, ""
	})

	//// Shapes ////
	////// TODO RE-ADD SHAPES
	shapsMapping := map[string][]*Shape{}
	// for _, shape := range g.Shapes {
	// 	shapsMapping[shape.ID] = append(shapsMapping[shape.ID], &shape)
	// }

	//// Routes / Services ////
	g.ctdfServices = map[string]*ctdf.Service{}
	g.routeMap = map[string]Route{}
	importObject[Route](g, "routes.txt", "services_raw", dataset.SupportedObjects.Services, func(r Route) (any, string) {
		g.routeMap[r.ID] = r
		serviceID := fmt.Sprintf("%s-service-%s", dataset.Identifier, r.ID)

		serviceName := g.GetTranslation("routes", "route_short_name", "en", r.ShortName)
		if serviceName == "" {
			serviceName = g.GetTranslation("routes", "route_long_name", "en", r.LongName)
		}

		operatorRef := agencyNOCMapping[r.AgencyID]
		if operatorRef == "" {
			operatorRef = fmt.Sprintf("%s-operator-%s", dataset.Identifier, r.AgencyID)
		}

		if util.ContainsString(dataset.IgnoreObjects.Services.ByOperator, operatorRef) {
			return nil, ""
		}

		ctdfService := &ctdf.Service{
			PrimaryIdentifier: serviceID,
			OtherIdentifiers: []string{
				fmt.Sprintf("gtfs-route-%s", r.ID),
			},
			CreationDateTime:     time.Now(),
			ModificationDateTime: time.Now(),
			DataSource:           datasource,
			ServiceName:          serviceName,
			OperatorRef:          operatorRef,
			Routes:               []ctdf.Route{},
			BrandColour:          r.Colour,
			SecondaryBrandColour: r.TextColour,
			TransportType:        convertTransportType(r.Type),
		}

		transforms.Transform(ctdfService, 1, "gb-dft-bods-gtfs-schedule")

		g.ctdfServices[r.ID] = ctdfService

		return ctdfService, serviceID
	})

	//// Journeys / Trips ////
	g.ctdfJourneys = map[string]*ctdf.Journey{}
	// fullJourneyTracks := map[string][]ctdf.Location{}

	importObject[Trip](g, "trips.txt", "journeys", false, func(t Trip) (any, string) {
		journeyID := fmt.Sprintf("%s-journey-%s", dataset.Identifier, t.ID)
		serviceID := fmt.Sprintf("%s-service-%s", dataset.Identifier, t.RouteID)

		if g.ctdfServices[t.RouteID] == nil {
			log.Debug().Str("trip", t.ID).Str("route", t.RouteID).Msg("Cannot find service for this trip")
			return nil, ""
		}

		operatorRef := g.ctdfServices[t.RouteID].OperatorRef

		if util.ContainsString(dataset.IgnoreObjects.Services.ByOperator, operatorRef) {
			return nil, ""
		}

		availability := &ctdf.Availability{}
		// Calendar availability
		calendar, exists := g.calendarMapping[t.ServiceID]
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
		for _, calendarDate := range g.calendarDateMapping[t.ServiceID] {
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
		g.ctdfJourneys[t.ID] = &ctdf.Journey{
			PrimaryIdentifier: journeyID,
			OtherIdentifiers: map[string]string{
				"GTFS-TripID":  t.ID,
				"GTFS-RouteID": t.RouteID,
			},
			CreationDateTime:     time.Now(),
			ModificationDateTime: time.Now(),
			DataSource:           datasource,
			ServiceRef:           serviceID,
			OperatorRef:          operatorRef,
			// Direction:            trip.DirectionID,
			DestinationDisplay: g.GetTranslation("trips", "trip_headsign", "en", t.Headsign),
			DepartureTimezone:  agenciesMap[g.routeMap[t.RouteID].AgencyID].Timezone,
			Availability:       availability,
			Path:               []*ctdf.JourneyPathItem{},
		}

		if t.BlockID != "" {
			g.ctdfJourneys[t.ID].OtherIdentifiers["BlockNumber"] = t.BlockID
		}

		if t.ShapeID != "" {
			journeyTrack := []ctdf.Location{}

			shapes := shapsMapping[t.ShapeID]

			// Make sure its in order
			sort.Slice(shapes, func(i, j int) bool {
				return shapes[i].PointSequence < shapes[j].PointSequence
			})

			for _, shape := range shapes {
				journeyTrack = append(journeyTrack, ctdf.Location{
					Type:        "Point",
					Coordinates: []float64{shape.PointLongitude, shape.PointLatitude},
				})
			}

			// fullJourneyTracks[trip.ID] = journeyTrack
			g.ctdfJourneys[t.ID].Track = journeyTrack
		}

		return nil, ""
	})

	//// Stop Times ////
	g.tripStopSequenceMap = map[string]map[int]*StopTime{}
	importObject[StopTime](g, "stop_times.txt", "stop_times", false, func(stopTime StopTime) (any, string) {
		if _, exists := g.tripStopSequenceMap[stopTime.TripID]; !exists {
			g.tripStopSequenceMap[stopTime.TripID] = map[int]*StopTime{}
		}
		g.tripStopSequenceMap[stopTime.TripID][stopTime.StopSequence] = &stopTime

		return nil, ""
	})

	log.Info().Msg("Importing Finished Journeys")
	journeysQueue := NewDatabaseBatchProcessingQueue("journeys", 1*time.Second, 1*time.Minute, 2000)
	if dataset.SupportedObjects.Journeys {
		journeysQueue.Process()
	}

	for tripID, tripSequencyMap := range g.tripStopSequenceMap {
		if g.ctdfJourneys[tripID] == nil {
			log.Debug().Str("trip", tripID).Msg("Cannot find journey for this trip")
			continue
		}

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
				originStopRef = fmt.Sprintf("gb-atco-%s", previousStopTime.StopID)
				destinationStopRef = fmt.Sprintf("gb-atco-%s", stopTime.StopID)
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

			g.ctdfJourneys[tripID].Path = append(g.ctdfJourneys[tripID].Path, journeyPathItem)

			if index == 1 {
				g.ctdfJourneys[tripID].DepartureTime = originDeparturelTime
			}
		}

		// TODO fix transforms here
		// transforms.Transform(g.ctdfJourneys[tripID], 1, "gb-dft-bods-gtfs-schedule")
		if util.ContainsString([]string{
			"gb-noc-LDLR", "gb-noc-LULD", "gb-noc-TRAM", "gb-dft-bods-gtfs-schedule-operator-OPTEMP454",
			"gb-noc-ABLO", "gb-dft-bods-gtfs-schedule-operator-OP12046", "gb-noc-ALNO", "gb-noc-ALSO", "gb-dft-bods-gtfs-schedule-operator-OPTEMP450", "gb-dft-bods-gtfs-schedule-operator-OP11684",
			"gb-noc-ELBG", "gb-dft-bods-gtfs-schedule-operator-OPTEMP456", "gb-dft-bods-gtfs-schedule-operator-OP3039", "gb-noc-LSOV", "gb-noc-LUTD", "gb-dft-bods-gtfs-schedule-operator-OP2974",
			"gb-noc-MTLN", "gb-noc-SULV",
		}, g.ctdfJourneys[tripID].OperatorRef) || (g.ctdfJourneys[tripID].OperatorRef == "gb-noc-UNIB" && util.ContainsString([]string{
			"gb-dft-bods-gtfs-schedule-service-14023", "gb-dft-bods-gtfs-schedule-service-13950", "gb-dft-bods-gtfs-schedule-service-14053", "gb-dft-bods-gtfs-schedule-service-13966", "gb-dft-bods-gtfs-schedule-service-13968", "gb-dft-bods-gtfs-schedule-service-82178",
		}, g.ctdfJourneys[tripID].ServiceRef)) {
			g.ctdfJourneys[tripID].OperatorRef = "gb-noc-TFLO"
		}

		// Insert
		if dataset.SupportedObjects.Journeys {
			bsonRep, _ := bson.Marshal(bson.M{"$set": g.ctdfJourneys[tripID]})
			updateModel := mongo.NewUpdateOneModel()
			updateModel.SetFilter(bson.M{"primaryidentifier": g.ctdfJourneys[tripID].PrimaryIdentifier})
			updateModel.SetUpdate(bsonRep)
			updateModel.SetUpsert(true)

			journeysQueue.Add(updateModel)
		}

		g.ctdfJourneys[tripID] = nil
	}

	if dataset.SupportedObjects.Journeys {
		journeysQueue.Wait()
	}

	log.Info().Msg("Finished Journeys")

	return nil
}

func (gtfs *Schedule) GetTranslation(table string, field string, language string, originalValue string) string {
	for _, translation := range gtfs.Translations {
		if translation.TableName == table && translation.FieldName == field && translation.Language == language && translation.FieldValue == originalValue {
			return translation.Translation
		}
	}

	return originalValue
}

func convertTransportType(intType int) ctdf.TransportType {
	routeTypeMapping := map[int]ctdf.TransportType{
		0:    ctdf.TransportTypeTram,
		1:    ctdf.TransportTypeMetro,
		2:    ctdf.TransportTypeRail,
		3:    ctdf.TransportTypeBus,
		4:    ctdf.TransportTypeFerry,
		5:    ctdf.TransportTypeTram,
		6:    ctdf.TransportTypeCableCar,
		7:    ctdf.TransportTypeFunicular,
		11:   ctdf.TransportTypeTram,
		12:   ctdf.TransportTypeRail, // Monorail
		200:  ctdf.TransportTypeCoach,
		1000: ctdf.TransportTypeFerry,
		// 1100: ctdf.TransportTypeAir,
		1200: ctdf.TransportTypeFerry,
		1400: ctdf.TransportTypeFunicular,
	}

	transportType := routeTypeMapping[intType]
	if transportType != "" {
		return transportType
	}

	// Extended https://developers.google.com/transit/gtfs/reference/extended-route-types
	if intType >= 100 && intType <= 117 {
		return ctdf.TransportTypeRail
	} else if intType >= 200 && intType <= 209 {
		return ctdf.TransportTypeCoach
	} else if intType >= 400 && intType <= 405 {
		return ctdf.TransportTypeMetro
	} else if intType >= 700 && intType <= 716 {
		return ctdf.TransportTypeBus
	} else if intType >= 900 && intType <= 906 {
		return ctdf.TransportTypeTram
	} else if intType >= 1300 && intType <= 1307 {
		return ctdf.TransportTypeCableCar
	} else {
		return ctdf.TransportTypeUnknown
	}
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
