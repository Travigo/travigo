package gtfs

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
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
)

type Schedule struct {
	fileMap map[string]string

	Agencies []Agency

	calendarMapping     map[string]*Calendar
	calendarDateMapping map[string][]*CalendarDate

	ctdfServices  map[string]*ctdf.Service
	routeMap      map[string]Route
	stopLocations map[string]ctdf.Location
	frequencies   map[string][]ctdf.JourneyFrequency

	// translationIndex maps "table\x00field\x00language\x00value" -> translated value
	// for O(1) lookups instead of a linear scan of every translation per record.
	translationIndex map[string]string
}

type journeyTrackStore struct {
	dataset    datasets.DataSet
	datasource *ctdf.DataSourceReference
	queue      DatabaseBatchProcessingQueue
	unique     int
	reused     int
}

const (
	journeyTrackBatchSize      = 50
	journeyBatchSize           = 400
	maxPathPatternCacheEntries = 50000
	maxShapeSnapCacheEntries   = 50000
)

type boundedPathPatternCache struct {
	entries   map[[sha256.Size]byte]bool
	order     [][sha256.Size]byte
	next      int
	max       int
	evictions int
}

func newBoundedPathPatternCache(max int) *boundedPathPatternCache {
	return &boundedPathPatternCache{
		entries: make(map[[sha256.Size]byte]bool, max),
		order:   make([][sha256.Size]byte, 0, max),
		max:     max,
	}
}

func (cache *boundedPathPatternCache) Get(key [sha256.Size]byte) (bool, bool) {
	split, exists := cache.entries[key]
	return split, exists
}

func (cache *boundedPathPatternCache) Put(key [sha256.Size]byte, split bool) {
	if cache.max <= 0 {
		return
	}
	if _, exists := cache.entries[key]; exists {
		cache.entries[key] = split
		return
	}
	if len(cache.order) < cache.max {
		cache.order = append(cache.order, key)
	} else {
		delete(cache.entries, cache.order[cache.next])
		cache.order[cache.next] = key
		cache.next = (cache.next + 1) % cache.max
		cache.evictions++
	}
	cache.entries[key] = split
}

func journeyPathPatternHash(shapeID string, stopTimes []StopTime) [sha256.Size]byte {
	hasher := sha256.New()
	_, _ = io.WriteString(hasher, shapeID)
	for _, stopTime := range stopTimes {
		_, _ = hasher.Write([]byte{'\x00'})
		_, _ = io.WriteString(hasher, stopTime.StopID)
	}
	var hash [sha256.Size]byte
	copy(hash[:], hasher.Sum(nil))
	return hash
}

func newJourneyTrackStore(dataset datasets.DataSet, datasource *ctdf.DataSourceReference) *journeyTrackStore {
	store := &journeyTrackStore{dataset: dataset, datasource: datasource, queue: NewDatabaseBatchProcessingQueue("journey_tracks", time.Second, time.Minute, journeyTrackBatchSize)}
	store.queue.Process()
	return store
}

func (store *journeyTrackStore) Identifier(key string) string {
	hash := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%s-journey-track-%x", store.dataset.Identifier, hash)
}

func (store *journeyTrackStore) Reference(key string, track []ctdf.Location) string {
	if len(track) < 2 {
		return ""
	}
	hash := sha256.Sum256([]byte(key))
	ref := fmt.Sprintf("%s-journey-track-%x", store.dataset.Identifier, hash)
	store.unique++
	object := &ctdf.JourneyTrack{PrimaryIdentifier: ref, Track: track, DataSource: store.datasource, CreationDateTime: time.Now(), ModificationDateTime: time.Now()}
	update := mongo.NewUpdateOneModel().SetFilter(bson.M{"primaryidentifier": ref}).SetUpdate(bson.M{"$set": object}).SetUpsert(true)
	store.queue.Add(update)
	return ref
}

func (store *journeyTrackStore) Wait() { store.queue.Wait() }

func (gtfs *Schedule) ParseFile(reader io.Reader) error {
	gtfs.fileMap = map[string]string{}

	// PERF(high-risk, NOT APPLIED): this currently writes the whole input reader to a
	// temp zip file and then copies every zip entry out to its own temp file (2+ full
	// copies on disk, plus the I/O cost). The entries could instead be parsed directly
	// via zip.NewReader over an io.ReaderAt / zip.OpenReader, reading each entry's
	// io.ReadCloser lazily inside importObject rather than materialising per-entry temp
	// files. NOT APPLIED: this changes the file lifecycle (the zip reader must stay
	// open for the whole Import, entries are streamed once and cannot be re-read, and
	// importObject currently os.Remove()s its temp file when done). Reworking this
	// safely requires changing how Import holds open the archive and how importObject
	// obtains its reader, which risks altering behaviour/ordering and leaking handles.
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
	processingQueue := NewDatabaseBatchProcessingQueue(tableName, 1*time.Second, 10*time.Second, 3000)
	if toProcess {
		processingQueue.Process()
	}

	file, _ := os.Open(g.fileMap[fileName])
	defer file.Close()

	// Check for UTF-8 BOM that messes up headers
	reader := bufio.NewReader(file)

	if bytes, _ := reader.Peek(3); len(bytes) == 3 &&
		bytes[0] == 0xEF &&
		bytes[1] == 0xBB &&
		bytes[2] == 0xBF {
		_, _ = reader.Discard(3)
	}

	decoder := csv.NewDecoder(reader)

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

type compactShapePoint struct {
	longitude float64
	latitude  float64
}

type sequencedShapePoint struct {
	compactShapePoint
	sequence int
}

func materializeShapeTrack(points []compactShapePoint) []ctdf.Location {
	track := make([]ctdf.Location, len(points))
	for index, point := range points {
		track[index] = ctdf.Location{Type: "Point", Coordinates: []float64{point.longitude, point.latitude}}
	}
	return track
}

// loadShapeTracks retains each point as two floats rather than a Location with
// a string, slice header and coordinate allocation. Full CTDF tracks are only
// materialised briefly when written or used for a new stop pattern.
func (g *Schedule) loadShapeTracks() (map[string][]compactShapePoint, error) {
	shapeTracks := map[string][]compactShapePoint{}
	if _, exists := g.fileMap["shapes.txt"]; !exists {
		log.Warn().Msg("GTFS feed does not contain shapes.txt")
		return shapeTracks, nil
	}

	shapePoints := map[string][]sequencedShapePoint{}
	if err := importObject[Shape](g, "shapes.txt", "shapes", false, func(shape Shape) (any, string) {
		shapePoints[shape.ID] = append(shapePoints[shape.ID], sequencedShapePoint{
			compactShapePoint: compactShapePoint{longitude: shape.PointLongitude, latitude: shape.PointLatitude},
			sequence:          shape.PointSequence,
		})
		return nil, ""
	}); err != nil {
		return nil, err
	}

	for shapeID, points := range shapePoints {
		sort.Slice(points, func(i, j int) bool {
			return points[i].sequence < points[j].sequence
		})

		track := make([]compactShapePoint, len(points))
		for index, point := range points {
			track[index] = point.compactShapePoint
		}
		shapeTracks[shapeID] = track
		delete(shapePoints, shapeID)
	}

	return shapeTracks, nil
}

func (g *Schedule) Import(dataset datasets.DataSet, datasource *ctdf.DataSourceReference) error {
	log.Info().Msg("Converting & Importing as CTDF into MongoDB")

	//// Translations ////
	g.translationIndex = map[string]string{}
	importObject[Translation](g, "translations.txt", "translations", false, func(t Translation) (any, string) {
		g.translationIndex[t.TableName+"\x00"+t.FieldName+"\x00"+t.Language+"\x00"+t.FieldValue] = t.Translation

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
			Email:                a.Email,
			PhoneNumber:          a.Phone,
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
	g.stopLocations = map[string]ctdf.Location{}
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
			Descriptor:           s.Description,
			Website:              s.URL,
			LocationType:         s.Type,
			PlatformCode:         s.PlatformCode,
			WheelchairBoarding:   s.Wheelchair,
			Location: &ctdf.Location{
				Type:        "Point",
				Coordinates: []float64{s.Longitude, s.Latitude},
			},
			Active:   true,
			Timezone: timezone,
		}
		if s.Parent != "" {
			ctdfStop.Associations = append(ctdfStop.Associations, &ctdf.Association{Type: "stop_group", AssociatedIdentifier: fmt.Sprintf("%s-stopgroup-%s", dataset.Identifier, s.Parent)})
		}
		g.stopLocations[s.ID] = *ctdfStop.Location

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

	//// Frequencies ////
	g.frequencies = map[string][]ctdf.JourneyFrequency{}
	if _, exists := g.fileMap["frequencies.txt"]; exists {
		if err := importObject[Frequency](g, "frequencies.txt", "frequencies", false, func(f Frequency) (any, string) {
			start, startErr := parseGTFSTime(f.StartTime)
			end, endErr := parseGTFSTime(f.EndTime)
			if startErr != nil || endErr != nil {
				log.Warn().Str("trip", f.TripID).Msg("Skipping GTFS frequency with invalid time")
				return nil, ""
			}
			g.frequencies[f.TripID] = append(g.frequencies[f.TripID], ctdf.JourneyFrequency{StartTime: start, EndTime: end, HeadwaySeconds: f.HeadwaySeconds, ExactTimes: parseExactTimes(f.ExactTimes)})
			return nil, ""
		}); err != nil {
			return err
		}
	}

	//// Shapes ////
	shapeTracks, err := g.loadShapeTracks()
	if err != nil {
		return err
	}
	trackStore := newJourneyTrackStore(dataset, datasource)
	for shapeID, track := range shapeTracks {
		trackStore.Reference("shape:"+shapeID, materializeShapeTrack(track))
	}
	shapeSnapCaches := make(map[string]map[string]cachedTrackSnap)
	shapeSnapCacheEntries := 0
	shapeSnapCacheEvictions := 0
	pathTrackCache := newBoundedPathPatternCache(maxPathPatternCacheEntries)
	pathPatternCacheHits := 0
	pathPatternCacheMisses := 0

	//// Transfers ////
	if _, exists := g.fileMap["transfers.txt"]; exists {
		if err := importObject[Transfer](g, "transfers.txt", "stop_transfers", true, func(t Transfer) (any, string) {
			fromStopRef := gtfsStopReference(dataset.Identifier, t.FromStopID)
			toStopRef := gtfsStopReference(dataset.Identifier, t.ToStopID)
			identifier := fmt.Sprintf("%s-%s-%s-%s-%s-%s", fromStopRef, toStopRef, t.FromRouteID, t.ToRouteID, t.FromTripID, t.ToTripID)
			transfer := &ctdf.StopTransfer{
				PrimaryIdentifier: identifier,
				FromStopRef:       fromStopRef, ToStopRef: toStopRef,
				FromRouteRef: fmt.Sprintf("%s-service-%s", dataset.Identifier, t.FromRouteID),
				ToRouteRef:   fmt.Sprintf("%s-service-%s", dataset.Identifier, t.ToRouteID),
				FromTripRef:  fmt.Sprintf("%s-journey-%s", dataset.Identifier, t.FromTripID),
				ToTripRef:    fmt.Sprintf("%s-journey-%s", dataset.Identifier, t.ToTripID),
				Type:         convertTransferType(t.TransferType), MinChangeDurationSeconds: t.MinTransferTime,
				CreationDateTime: time.Now(), ModificationDateTime: time.Now(), DataSource: datasource,
			}
			return transfer, identifier
		}); err != nil {
			return err
		}
	}

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
			Description:          r.Description,
			Website:              r.URL,
			NetworkRef:           r.NetworkID,
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
	gtfsTrips := map[string]Trip{}
	shapeTripUseCounts := map[string]int{}
	tripStringPool := map[string]string{}
	internTripString := func(value string) string {
		if value == "" {
			return ""
		}
		if interned, exists := tripStringPool[value]; exists {
			return interned
		}
		tripStringPool[value] = value
		return value
	}
	// PERF(medium-risk): cache the parsed+formatted calendar date-range condition
	// value keyed by calendar ServiceID. Many trips share the same calendar, and the
	// original code re-parsed calendar.Start/calendar.End via time.Parse for every
	// trip. We cache the exact final string ("from:to") so results are byte-for-byte
	// identical to the previous per-trip computation, including how time.Parse errors
	// (zero time -> "0001-01-01") would format.
	calendarConditionCache := map[string]string{}

	buildJourney := func(t Trip) *ctdf.Journey {
		journeyID := fmt.Sprintf("%s-journey-%s", dataset.Identifier, t.ID)
		serviceID := fmt.Sprintf("%s-service-%s", dataset.Identifier, t.RouteID)
		operatorRef := g.ctdfServices[t.RouteID].OperatorRef

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

			// PERF(medium-risk): reuse the cached parsed/formatted date-range value
			// for this calendar instead of re-parsing the same Start/End strings for
			// every trip on the calendar. The cached string is produced by the exact
			// same time.Parse + Format pipeline, so the result is unchanged.
			conditionValue, cached := calendarConditionCache[t.ServiceID]
			if !cached {
				dateRunsFrom, _ := time.Parse("20060102", calendar.Start)
				dateRunsTo, _ := time.Parse("20060102", calendar.End)
				conditionValue = fmt.Sprintf("%s:%s", dateRunsFrom.Format("2006-01-02"), dateRunsTo.Format("2006-01-02"))
				calendarConditionCache[t.ServiceID] = conditionValue
			}

			availability.Condition = append(availability.Condition, ctdf.AvailabilityRule{
				Type:  ctdf.AvailabilityDateRange,
				Value: conditionValue,
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
		journey := &ctdf.Journey{
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
			Direction:            strconv.FormatBool(t.DirectionID),
			ShortName:            t.Name,
			WheelchairAccessible: t.WheelchairAccessible,
			BikesAllowed:         t.BikesAllowed,
			Frequency:            g.frequencies[t.ID],
			DestinationDisplay:   g.GetTranslation("trips", "trip_headsign", "en", t.Headsign),
			DepartureTimezone:    agenciesMap[g.routeMap[t.RouteID].AgencyID].Timezone,
			Availability:         availability,
			Path:                 []*ctdf.JourneyPathItem{},
		}

		if t.BlockID != "" {
			journey.OtherIdentifiers["BlockNumber"] = t.BlockID
		}

		if _, exists := shapeTracks[t.ShapeID]; t.ShapeID != "" && exists {
			journey.TrackRef = trackStore.Identifier("shape:" + t.ShapeID)
		}

		return journey
	}

	importObject[Trip](g, "trips.txt", "journeys", false, func(t Trip) (any, string) {
		if g.ctdfServices[t.RouteID] == nil {
			log.Debug().Str("trip", t.ID).Str("route", t.RouteID).Msg("Cannot find service for this trip")
			return nil, ""
		}

		operatorRef := g.ctdfServices[t.RouteID].OperatorRef

		if util.ContainsString(dataset.IgnoreObjects.Services.ByOperator, operatorRef) {
			return nil, ""
		}
		t.RouteID = internTripString(t.RouteID)
		t.ServiceID = internTripString(t.ServiceID)
		t.Headsign = internTripString(t.Headsign)
		t.BlockID = internTripString(t.BlockID)
		t.ShapeID = internTripString(t.ShapeID)

		gtfsTrips[t.ID] = t
		if t.ShapeID != "" {
			shapeTripUseCounts[t.ShapeID]++
		}

		return nil, ""
	})
	tripStringPool = nil

	//// Stop Times ////
	stopTimeGroups, err := newSortedStopTimeGroups(g.fileMap["stop_times.txt"])
	if err != nil {
		return err
	}
	defer stopTimeGroups.Close()

	log.Info().Msg("Importing Finished Journeys")
	journeysQueue := NewDatabaseBatchProcessingQueue("journeys", 1*time.Second, 1*time.Minute, journeyBatchSize)
	if dataset.SupportedObjects.Journeys {
		journeysQueue.Process()
	}

	useGBAtcoStopRef := strings.Contains(dataset.Identifier, "gb-dft-bods-gtfs-schedule-")
	datasetStopPrefix := fmt.Sprintf("%s-stop-", dataset.Identifier)

	if err := stopTimeGroups.Process(func(tripID string, stopTimes []StopTime) error {
		trip, exists := gtfsTrips[tripID]
		if !exists {
			log.Debug().Str("trip", tripID).Msg("Cannot find journey for this trip")
			return nil
		}

		journey := buildJourney(trip)

		if len(stopTimes) > 1 {
			journey.Path = make([]*ctdf.JourneyPathItem, 0, len(stopTimes)-1)
		}

		for index := 1; index < len(stopTimes); index += 1 {
			stopTime := stopTimes[index]
			previousStopTime := stopTimes[index-1]

			originArrivalTime, err := parseGTFSTime(previousStopTime.ArrivalTime)
			if err != nil {
				log.Error().Err(err).Msg("Failed to parse previousStopTime.ArrivalTime")
			}
			originDeparturelTime, err := parseGTFSTime(previousStopTime.DepartureTime)
			if err != nil {
				log.Error().Err(err).Msg("Failed to parse previousStopTime.DepartureTime")
			}
			destinationArrivalTime, err := parseGTFSTime(stopTime.ArrivalTime)
			if err != nil {
				log.Error().Err(err).Msg("Failed to parse stopTime.ArrivalTime")
			}

			var originStopRef string
			var destinationStopRef string

			// TODO no hardocded nonsense!!
			if useGBAtcoStopRef {
				originStopRef = fmt.Sprintf("gb-atco-%s", previousStopTime.StopID)
				destinationStopRef = fmt.Sprintf("gb-atco-%s", stopTime.StopID)
			} else {
				originStopRef = datasetStopPrefix + previousStopTime.StopID
				destinationStopRef = datasetStopPrefix + stopTime.StopID
			}

			journeyPathItem := &ctdf.JourneyPathItem{
				OriginStopRef:          originStopRef,
				DestinationStopRef:     destinationStopRef,
				OriginArrivalTime:      originArrivalTime,
				DestinationArrivalTime: destinationArrivalTime,
				OriginDepartureTime:    originDeparturelTime,
				DestinationDisplay:     stopTime.StopHeadsign,
				OriginActivity:         make([]ctdf.JourneyPathItemActivity, 0, 2),
				DestinationActivity:    make([]ctdf.JourneyPathItemActivity, 0, 2),
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

			journey.Path = append(journey.Path, journeyPathItem)

			if index == 1 {
				journey.DepartureTime = originDeparturelTime
			}
		}

		patternHash := journeyPathPatternHash(trip.ShapeID, stopTimes)
		patternSplit, patternCached := pathTrackCache.Get(patternHash)
		if patternCached {
			pathPatternCacheHits++
			if patternSplit {
				trackStore.reused += len(journey.Path)
				journey.Track = nil
				journey.TrackRef = ""
				for index, path := range journey.Path {
					path.TrackRef = trackStore.Identifier(fmt.Sprintf("shape-leg:%s:%x:%d", trip.ShapeID, patternHash, index))
				}
			}
		} else if compactTrack := shapeTracks[trip.ShapeID]; len(compactTrack) > 1 {
			pathPatternCacheMisses++
			journeyTrack := materializeShapeTrack(compactTrack)
			var snapCache map[string]cachedTrackSnap
			if len(stopTimes) <= maxShapeSnapCacheEntries {
				if shapeSnapCacheEntries+len(stopTimes) > maxShapeSnapCacheEntries {
					shapeSnapCaches = make(map[string]map[string]cachedTrackSnap)
					shapeSnapCacheEntries = 0
					shapeSnapCacheEvictions++
				}
				snapCache = shapeSnapCaches[trip.ShapeID]
				if snapCache == nil {
					snapCache = map[string]cachedTrackSnap{}
					shapeSnapCaches[trip.ShapeID] = snapCache
				}
			}
			snapCacheEntriesBefore := len(snapCache)
			if assignJourneyPathTracksCached(journey.Path, stopTimes, g.stopLocations, journeyTrack, snapCache) {
				shapeSnapCacheEntries += len(snapCache) - snapCacheEntriesBefore
				// Every leg now owns an accurate slice of the shape, so avoid storing
				// the same geometry again at journey level. If splitting fails, the
				// complete journey track is intentionally retained as the fallback.
				journey.Track = nil
				journey.TrackRef = ""
				for index, path := range journey.Path {
					path.TrackRef = trackStore.Reference(fmt.Sprintf("shape-leg:%s:%x:%d", trip.ShapeID, patternHash, index), path.Track)
					path.Track = nil
				}
				pathTrackCache.Put(patternHash, true)
			} else {
				shapeSnapCacheEntries += len(snapCache) - snapCacheEntriesBefore
				pathTrackCache.Put(patternHash, false)
			}
		}

		// TODO fix transforms here
		// transforms.Transform(journey, 1, "gb-dft-bods-gtfs-schedule")
		// if util.ContainsString([]string{
		// 	"gb-noc-LDLR", "gb-noc-LULD", "gb-noc-TRAM", "gb-dft-bods-gtfs-schedule-operator-OPTEMP454",
		// 	"gb-noc-ABLO", "gb-dft-bods-gtfs-schedule-operator-OP12046", "gb-noc-ALNO", "gb-noc-ALSO", "gb-dft-bods-gtfs-schedule-operator-OPTEMP450", "gb-dft-bods-gtfs-schedule-operator-OP11684",
		// 	"gb-noc-ELBG", "gb-dft-bods-gtfs-schedule-operator-OPTEMP456", "gb-dft-bods-gtfs-schedule-operator-OP3039", "gb-noc-LSOV", "gb-noc-LUTD", "gb-dft-bods-gtfs-schedule-operator-OP2974",
		// 	"gb-noc-MTLN", "gb-noc-SULV",
		// }, journey.OperatorRef) || (journey.OperatorRef == "gb-noc-UNIB" && util.ContainsString([]string{
		// 	"gb-dft-bods-gtfs-schedule-service-14023", "gb-dft-bods-gtfs-schedule-service-13950", "gb-dft-bods-gtfs-schedule-service-14053", "gb-dft-bods-gtfs-schedule-service-13966", "gb-dft-bods-gtfs-schedule-service-13968", "gb-dft-bods-gtfs-schedule-service-82178",
		// }, journey.ServiceRef)) {
		// 	journey.OperatorRef = "gb-noc-TFLO"
		// }

		// Insert
		if dataset.SupportedObjects.Journeys {
			// Geometry is persisted exclusively in journey_tracks. Keep the
			// in-memory fallback available for this import, but never write the
			// legacy embedded fields back to MongoDB.
			journey.Track = nil
			for _, path := range journey.Path {
				if path != nil {
					path.Track = nil
				}
			}
			bsonRep, _ := bson.Marshal(bson.M{"$set": journey})
			updateModel := mongo.NewUpdateOneModel()
			updateModel.SetFilter(bson.M{"primaryidentifier": journey.PrimaryIdentifier})
			updateModel.SetUpdate(bsonRep)
			updateModel.SetUpsert(true)

			journeysQueue.Add(updateModel)
		}

		delete(gtfsTrips, tripID)
		if trip.ShapeID != "" {
			shapeTripUseCounts[trip.ShapeID]--
			if shapeTripUseCounts[trip.ShapeID] <= 0 {
				delete(shapeTripUseCounts, trip.ShapeID)
				delete(shapeTracks, trip.ShapeID)
				if cache := shapeSnapCaches[trip.ShapeID]; cache != nil {
					shapeSnapCacheEntries -= len(cache)
					delete(shapeSnapCaches, trip.ShapeID)
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	gtfsTrips = nil

	if dataset.SupportedObjects.Journeys {
		journeysQueue.Wait()
	}
	trackStore.Wait()
	log.Info().Str("dataset", dataset.Identifier).
		Int("unique_tracks", trackStore.unique).
		Int("reused_tracks", trackStore.reused).
		Int("snap_cache_entries", shapeSnapCacheEntries).
		Int("snap_cache_evictions", shapeSnapCacheEvictions).
		Int("path_pattern_hits", pathPatternCacheHits).
		Int("path_pattern_misses", pathPatternCacheMisses).
		Int("path_pattern_cache_evictions", pathTrackCache.evictions).
		Msg("Finished GTFS journey track processing")

	log.Info().Msg("Finished Journeys")

	return nil
}

func (gtfs *Schedule) GetTranslation(table string, field string, language string, originalValue string) string {
	if translation, exists := gtfs.translationIndex[table+"\x00"+field+"\x00"+language+"\x00"+originalValue]; exists {
		return translation
	}

	return originalValue
}

var routeTypeMapping = map[int]ctdf.TransportType{
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

func convertTransportType(intType int) ctdf.TransportType {
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

func parseGTFSTime(timestamp string) (time.Time, error) {
	splitTimestamp := strings.Split(timestamp, ":")
	if len(splitTimestamp) != 3 {
		return time.Time{}, fmt.Errorf("invalid GTFS time %q", timestamp)
	}
	hour, err := strconv.Atoi(splitTimestamp[0])
	if err != nil {
		return time.Time{}, err
	}
	minute, minuteErr := strconv.Atoi(splitTimestamp[1])
	second, secondErr := strconv.Atoi(splitTimestamp[2])
	if minuteErr != nil || secondErr != nil || hour < 0 || minute < 0 || minute > 59 || second < 0 || second > 59 {
		return time.Time{}, fmt.Errorf("invalid GTFS time %q", timestamp)
	}
	return time.Date(0, time.January, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute + time.Duration(second)*time.Second), nil
}

func parseExactTimes(value string) int8 {
	parsed, err := strconv.ParseInt(value, 10, 8)
	if err != nil {
		return 0
	}
	return int8(parsed)
}

func gtfsStopReference(datasetIdentifier string, stopID string) string {
	if strings.Contains(datasetIdentifier, "gb-dft-bods-gtfs-schedule-") {
		return fmt.Sprintf("gb-atco-%s", stopID)
	}
	return fmt.Sprintf("%s-stop-%s", datasetIdentifier, stopID)
}

func convertTransferType(value int8) ctdf.StopTransferType {
	switch value {
	case 1:
		return ctdf.StopTransferTypeTimed
	case 2:
		return ctdf.StopTransferTypeMinimumTime
	case 3:
		return ctdf.StopTransferTypeForbidden
	case 4:
		return ctdf.StopTransferTypeInSeat
	default:
		return ctdf.StopTransferTypeRecommended
	}
}
