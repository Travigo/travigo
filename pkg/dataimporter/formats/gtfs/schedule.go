package gtfs

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/go-csv"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/dataimporter/formats/gtfs/nyct"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
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
	stopNames     map[string]string
	frequencies   map[string][]frequencyWindow

	// translationIndex maps "table\x00field\x00language\x00value" -> translated value
	// for O(1) lookups instead of a linear scan of every translation per record.
	translationIndex map[string]string
}

type routeEndpointPair struct {
	Origin      string
	Destination string
}

func appendServiceRoute(service *ctdf.Service, seen map[routeEndpointPair]struct{}, origin, destination string) {
	if service == nil || origin == "" || destination == "" {
		return
	}
	pair := routeEndpointPair{Origin: origin, Destination: destination}
	if _, exists := seen[pair]; exists {
		return
	}
	seen[pair] = struct{}{}
	service.Routes = append(service.Routes, ctdf.Route{Origin: origin, Destination: destination})
}

func validGTFSCoordinates(longitude, latitude float64) bool {
	return !math.IsNaN(longitude) && !math.IsInf(longitude, 0) &&
		!math.IsNaN(latitude) && !math.IsInf(latitude, 0) &&
		longitude >= -180 && longitude <= 180 &&
		latitude >= -90 && latitude <= 90
}

type frequencyWindow struct {
	StartTime      time.Time
	EndTime        time.Time
	HeadwaySeconds int
	// ExactTimes is validated while importing. Both exact and frequency-based
	// windows are materialized into concrete static journey iterations here.
	ExactTimes int8
}

type journeyTrackStore struct {
	dataset    datasets.DataSet
	datasource *ctdf.DataSourceReference
	pending    []mongo.WriteModel
	unique     int
	reused     int
}

const (
	journeyTrackBatchSize      = 750
	journeyBatchSize           = 5000
	maxPathPatternCacheEntries = 60000
	maxShapeSnapCacheEntries   = 60000
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
	return &journeyTrackStore{dataset: dataset, datasource: datasource, pending: make([]mongo.WriteModel, 0, journeyTrackBatchSize)}
}

func (store *journeyTrackStore) Identifier(key string) string {
	hash := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%s-journey-track-%x", store.dataset.Identifier, hash)
}

func (store *journeyTrackStore) Reference(key string, track []ctdf.Location) (string, error) {
	if len(track) < 2 {
		return "", nil
	}
	hash := sha256.Sum256([]byte(key))
	ref := fmt.Sprintf("%s-journey-track-%x", store.dataset.Identifier, hash)
	store.unique++
	object := &ctdf.JourneyTrack{PrimaryIdentifier: ref, Track: track, DataSource: store.datasource, CreationDateTime: time.Now(), ModificationDateTime: time.Now()}
	update := mongo.NewUpdateOneModel().SetFilter(bson.M{"primaryidentifier": ref}).SetUpdate(bson.M{"$set": object}).SetUpsert(true)
	store.pending = append(store.pending, update)
	if len(store.pending) >= journeyTrackBatchSize {
		if err := store.Flush(); err != nil {
			return "", err
		}
	}
	return ref, nil
}

func (store *journeyTrackStore) Flush() error {
	if len(store.pending) == 0 {
		return nil
	}
	models := store.pending
	store.pending = make([]mongo.WriteModel, 0, journeyTrackBatchSize)
	if _, err := database.GetCollection("journey_tracks").BulkWrite(context.Background(), models); err != nil {
		return fmt.Errorf("store GTFS journey tracks: %w", err)
	}
	return nil
}

func flushGTFSJourneyBatch(trackStore *journeyTrackStore, journeyModels []mongo.WriteModel) error {
	if len(journeyModels) == 0 {
		return nil
	}
	if err := trackStore.Flush(); err != nil {
		return err
	}
	if _, err := database.GetCollection(database.JourneysRawCollectionName).BulkWrite(context.Background(), journeyModels); err != nil {
		return fmt.Errorf("store GTFS raw journeys: %w", err)
	}
	return nil
}

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
	// Performance Review [Set D] [PR036] [P1] (batch): The Copy results below are unchecked; a ZIP
	// checksum/read failure can leave truncated CSV that later stages accept. The
	// ZIP/temp archive handles are not closed, and only tables actually consumed by
	// importObject are removed, leaving unsupported entries/error-path files behind.
	// Own all extraction artifacts in one scoped directory, check copies/closes,
	// validate required tables, and clean every artifact on success or failure.
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
	if err = decodeGTFSHeader(decoder, line); err != nil {
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
			update := bson.M{"$set": parsedObject}
			if stop, ok := parsedObject.(*ctdf.Stop); ok && stop.Location == nil {
				// Location is tagged omitempty, so a nil value in $set would leave a
				// previous invalid location on an existing raw stop. Explicitly clear it.
				update["$unset"] = bson.M{"location": ""}
			}
			bsonRep, _ := bson.Marshal(update)
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
	// Performance Review [Set O] [PR037] [P2] (batch): Compact coordinates help, but this still
	// retains every shape before trips/operator exclusions are known, temporarily
	// alongside sequenced points. Load accepted trip metadata first, retain only
	// referenced shapes, and consider a shape-ID spool for feeds beyond the memory
	// budget. Account for trips without stop_times so their shapes are also released.
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

func (g *Schedule) Import(dataset datasets.DataSet, datasource *ctdf.DataSourceReference) (datasets.DataImportReport, error) {
	log.Info().Msg("Converting & Importing as CTDF into MongoDB")

	//// Translations ////
	// Performance Review [Set H] [PR038] [P1] (batch): Most importObject calls below discard errors
	// (agency/stops/calendars/routes/trips as well as translations). A malformed
	// present table can leave only a prefix imported while Import returns success,
	// enabling cleanup of valid previous rows and publication of the new hash.
	// Distinguish absent optional tables from parse/read failures, propagate required
	// failures, and cancel/drain any started writers before returning an error.
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
	g.stopNames = map[string]string{}
	importObject[Stop](g, "stops.txt", "stops_raw", dataset.SupportedObjects.Stops, func(s Stop) (any, string) {
		timezone := s.Timezone

		if timezone == "" {
			timezone = g.Agencies[0].Timezone
		}

		stopID := fmt.Sprintf("%s-stop-%s", dataset.Identifier, s.ID)
		stopName := g.GetTranslation("stops", "stop_name", "en", s.Name)
		g.stopNames[s.ID] = stopName
		ctdfStop := &ctdf.Stop{
			PrimaryIdentifier:    stopID,
			OtherIdentifiers:     []string{stopID},
			CreationDateTime:     time.Now(),
			ModificationDateTime: time.Now(),
			DataSource:           datasource,
			PrimaryName:          stopName,
			Descriptor:           s.Description,
			Website:              s.URL,
			LocationType:         s.Type,
			PlatformCode:         s.PlatformCode,
			WheelchairBoarding:   s.Wheelchair,
			Active:               true,
			Timezone:             timezone,
		}
		if validGTFSCoordinates(s.Longitude, s.Latitude) {
			ctdfStop.Location = &ctdf.Location{
				Type:        "Point",
				Coordinates: []float64{s.Longitude, s.Latitude},
			}
			g.stopLocations[s.ID] = *ctdfStop.Location
		} else {
			log.Warn().
				Str("dataset", dataset.Identifier).
				Str("stop_id", s.ID).
				Float64("longitude", s.Longitude).
				Float64("latitude", s.Latitude).
				Msg("Skipping invalid GTFS stop coordinates")
		}
		if s.Parent != "" {
			ctdfStop.Associations = append(ctdfStop.Associations, &ctdf.Association{Type: "stop_group", AssociatedIdentifier: fmt.Sprintf("%s-stopgroup-%s", dataset.Identifier, s.Parent)})
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

	//// Frequencies ////
	g.frequencies = map[string][]frequencyWindow{}
	if _, exists := g.fileMap["frequencies.txt"]; exists {
		if err := importObject[Frequency](g, "frequencies.txt", "frequencies", false, func(f Frequency) (any, string) {
			start, startErr := parseGTFSTime(f.StartTime)
			end, endErr := parseGTFSTime(f.EndTime)
			if startErr != nil || endErr != nil {
				log.Warn().Str("trip", f.TripID).Msg("Skipping GTFS frequency with invalid time")
				return nil, ""
			}
			if !start.Before(end) || f.HeadwaySeconds <= 0 {
				log.Warn().Str("trip", f.TripID).Msg("Skipping GTFS frequency with invalid window or headway")
				return nil, ""
			}
			exactTimes, exactTimesErr := parseExactTimes(f.ExactTimes)
			if exactTimesErr != nil {
				log.Warn().Str("trip", f.TripID).Err(exactTimesErr).Msg("Skipping GTFS frequency with invalid exact_times")
				return nil, ""
			}
			g.frequencies[f.TripID] = append(g.frequencies[f.TripID], frequencyWindow{StartTime: start, EndTime: end, HeadwaySeconds: f.HeadwaySeconds, ExactTimes: exactTimes})
			return nil, ""
		}); err != nil {
			return datasets.DataImportReport{}, err
		}
	}

	//// Shapes ////
	// Performance Review [Set F] [PR039] [P2] (batch): Shape loading/writes, trip retention and the
	// stop_times external sort run even when Journeys is disabled; only the final
	// journey write is gated. Build a dependency plan from SupportedObjects before
	// parsing, so operator/service-only imports skip unused geometry and stop times.
	// Preserve tables genuinely needed by requested outputs and gate track writes too.
	shapeTracks, err := g.loadShapeTracks()
	if err != nil {
		return datasets.DataImportReport{}, err
	}
	trackStore := newJourneyTrackStore(dataset, datasource)
	for shapeID, track := range shapeTracks {
		if _, err := trackStore.Reference("shape:"+shapeID, materializeShapeTrack(track)); err != nil {
			return datasets.DataImportReport{}, err
		}
	}
	shapeSnapCaches := make(map[string]map[string]cachedTrackSnap)
	shapeSnapCacheEntries := 0
	shapeSnapCacheEvictions := 0
	pathTrackCache := newBoundedPathPatternCache(maxPathPatternCacheEntries)
	pathPatternCacheHits := 0
	pathPatternCacheMisses := 0

	//// Transfers ////
	// Performance Review [Set H] [PR040] [P1] (batch): Transfers are upserted but the manager never
	// cleans stop_transfers for this dataset. Removed rules, including rules from a
	// later feed with no transfers.txt, survive and are all loaded by the planner.
	// Reconcile this table with the same validated full-feed generation, distinguish
	// optional-table absence from parsing failure, and retire obsolete rules only
	// after the new snapshot succeeds. This also bounds planner input growth.
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
			return datasets.DataImportReport{}, err
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

		if alias := dataset.OperatorAliases[operatorRef]; alias != "" {
			operatorRef = alias
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
		// Performance Review [Set F] [PR041] [P2] (batch): The date-range string is cached above,
		// but every trip still parses all calendar_dates exceptions and rebuilds
		// identical availability rules for its ServiceID. Cache a validated immutable
		// calendar template per service and copy only when journey-specific changes
		// are needed; preserve exception precedence and frequency-clone independence.
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

		// Get the agency so we can lookup timezone
		// If its not set we'll just assume the first agency is the one we want to use
		var agency *Agency
		if route, exists := g.routeMap[t.RouteID]; exists {
			if a, exists := agenciesMap[route.AgencyID]; exists {
				agency = a
			}
		}
		if agency == nil && len(g.Agencies) > 0 {
			agency = &g.Agencies[0]
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
			DestinationDisplay:   g.GetTranslation("trips", "trip_headsign", "en", t.Headsign),
			DepartureTimezone:    agency.Timezone,
			Availability:         availability,
		}

		if dataset.Identifier == nyct.SubwayDataset {
			if key := nyct.TripID(t.ID); key != "" {
				// Subway realtime IDs omit the schedule prefix. Retain the path
				// because origin time, route and direction can collide.
				journey.OtherIdentifiers["GTFS-TripID"] = key
			}
		}

		if t.BlockID != "" {
			journey.OtherIdentifiers["BlockNumber"] = t.BlockID
		}

		if len(shapeTracks[t.ShapeID]) > 1 {
			journey.TrackRef = trackStore.Identifier("shape:" + t.ShapeID)
		}

		return journey
	}

	importObject[Trip](g, "trips.txt", database.JourneysRawCollectionName, false, func(t Trip) (any, string) {
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
		return datasets.DataImportReport{}, err
	}
	defer stopTimeGroups.Close()

	log.Info().Msg("Importing Finished Journeys")
	journeyModels := make([]mongo.WriteModel, 0, journeyBatchSize)
	flushJourneys := func() error {
		if len(journeyModels) == 0 {
			return nil
		}
		models := journeyModels
		journeyModels = make([]mongo.WriteModel, 0, journeyBatchSize)
		// Track geometry must be durable before a journey batch can publish its refs.
		return flushGTFSJourneyBatch(trackStore, models)
	}
	importedJourneys := 0
	rejectedJourneys := 0
	rejectJourney := func(tripID string, err error) error {
		rejectedJourneys++
		if rejectedJourneys <= 5 {
			log.Warn().Err(err).Str("trip", tripID).Msg("Skipping GTFS journey with unresolved times")
		}
		return nil
	}
	serviceRouteKeys := make(map[string]map[routeEndpointPair]struct{}, len(g.ctdfServices))
	for routeID, service := range g.ctdfServices {
		keys := make(map[routeEndpointPair]struct{}, len(service.Routes))
		for _, route := range service.Routes {
			keys[routeEndpointPair{Origin: route.Origin, Destination: route.Destination}] = struct{}{}
		}
		serviceRouteKeys[routeID] = keys
	}

	useGBAtcoStopRef := strings.Contains(dataset.Identifier, "gb-dft-bods-gtfs-schedule-")
	datasetStopPrefix := fmt.Sprintf("%s-stop-", dataset.Identifier)

	if err := stopTimeGroups.Process(func(tripID string, stopTimes []StopTime) error {
		trip, exists := gtfsTrips[tripID]
		if !exists {
			log.Debug().Str("trip", tripID).Msg("Cannot find journey for this trip")
			return nil
		}
		if len(stopTimes) > 1 {
			appendServiceRoute(g.ctdfServices[trip.RouteID], serviceRouteKeys[trip.RouteID], g.stopNames[stopTimes[0].StopID], g.stopNames[stopTimes[len(stopTimes)-1].StopID])
		}

		journey := buildJourney(trip)
		journey.Calls = make([]ctdf.JourneyCall, len(stopTimes))
		for index, stopTime := range stopTimes {
			arrivalTime, err := parseOptionalGTFSTime(stopTime.ArrivalTime)
			if err != nil {
				return rejectJourney(tripID, err)
			}
			departureTime, err := parseOptionalGTFSTime(stopTime.DepartureTime)
			if err != nil {
				return rejectJourney(tripID, err)
			}

			stopRef := datasetStopPrefix + stopTime.StopID
			if useGBAtcoStopRef {
				stopRef = fmt.Sprintf("gb-atco-%s", stopTime.StopID)
			}
			call := ctdf.JourneyCall{
				StopRef:             stopRef,
				Arrival:             arrivalTime,
				Departure:           departureTime,
				OriginActivity:      make([]ctdf.JourneyPathItemActivity, 0, 2),
				DestinationActivity: make([]ctdf.JourneyPathItemActivity, 0, 2),
				Start:               index == 0,
				End:                 index == len(stopTimes)-1,
			}
			if index+1 < len(stopTimes) && stopTimes[index+1].StopHeadsign != "" {
				call.Outgoing = &ctdf.JourneyLeg{DestinationDisplay: stopTimes[index+1].StopHeadsign}
			}
			if stopTime.DropOffType == 0 {
				call.OriginActivity = append(call.OriginActivity, ctdf.JourneyPathItemActivitySetdown)
				call.DestinationActivity = append(call.DestinationActivity, ctdf.JourneyPathItemActivitySetdown)
			}
			if stopTime.PickupType == 0 {
				call.OriginActivity = append(call.OriginActivity, ctdf.JourneyPathItemActivityPickup)
				call.DestinationActivity = append(call.DestinationActivity, ctdf.JourneyPathItemActivityPickup)
			}
			journey.Calls[index] = call
			if index == 0 {
				journey.DepartureTime = departureTime
			}
		}
		if err := ctdf.InterpolateMissingJourneyTimes(journey); err != nil {
			return rejectJourney(tripID, err)
		}

		patternHash := journeyPathPatternHash(trip.ShapeID, stopTimes)
		patternSplit, patternCached := pathTrackCache.Get(patternHash)
		if patternCached {
			pathPatternCacheHits++
			if patternSplit {
				trackStore.reused += journey.LegCount()
				journey.Track = nil
				journey.TrackRef = ""
				for index := 0; index+1 < len(journey.Calls); index++ {
					if journey.Calls[index].Outgoing == nil {
						journey.Calls[index].Outgoing = &ctdf.JourneyLeg{}
					}
					journey.Calls[index].Outgoing.TrackRef = trackStore.Identifier(fmt.Sprintf("shape-leg:%s:%x:%d", trip.ShapeID, patternHash, index))
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
			if assignJourneyCallTracksCached(journey.Calls, stopTimes, g.stopLocations, journeyTrack, snapCache) {
				shapeSnapCacheEntries += len(snapCache) - snapCacheEntriesBefore
				// Every leg now owns an accurate slice of the shape, so avoid storing
				// the same geometry again at journey level. If splitting fails, the
				// complete journey track is intentionally retained as the fallback.
				journey.Track = nil
				journey.TrackRef = ""
				for index := 0; index+1 < len(journey.Calls); index++ {
					call := &journey.Calls[index]
					if call.Outgoing == nil {
						call.Outgoing = &ctdf.JourneyLeg{}
					}
					trackRef, err := trackStore.Reference(fmt.Sprintf("shape-leg:%s:%x:%d", trip.ShapeID, patternHash, index), call.Outgoing.Track)
					if err != nil {
						return fmt.Errorf("store GTFS shape leg track for trip %s: %w", tripID, err)
					}
					call.Outgoing.TrackRef = trackRef
					call.Outgoing.Track = nil
				}
				pathTrackCache.Put(patternHash, true)
			} else {
				shapeSnapCacheEntries += len(snapCache) - snapCacheEntriesBefore
				pathTrackCache.Put(patternHash, false)
			}
		}
		journey.RefreshStopRefs()

		if dataset.SupportedObjects.Journeys {
			// Insert one concrete journey for each frequency iteration. Frequency
			// trips are represented by their expanded journeys in CTDF; the base
			// template is never persisted.
			for _, expandedJourney := range expandJourneyForFrequencies(journey, g.frequencies[tripID]) {
				if err := expandedJourney.ValidatePath(); err != nil {
					return fmt.Errorf("validate GTFS journey %s: %w", expandedJourney.PrimaryIdentifier, err)
				}
				bsonRep, err := bson.Marshal(expandedJourney)
				if err != nil {
					return fmt.Errorf("marshal GTFS journey %s: %w", expandedJourney.PrimaryIdentifier, err)
				}
				replacementModel := mongo.NewReplaceOneModel()
				replacementModel.SetFilter(bson.M{"primaryidentifier": expandedJourney.PrimaryIdentifier})
				replacementModel.SetReplacement(bsonRep)
				replacementModel.SetUpsert(true)

				journeyModels = append(journeyModels, replacementModel)
				importedJourneys++
				if len(journeyModels) >= journeyBatchSize {
					if err := flushJourneys(); err != nil {
						return fmt.Errorf("flush GTFS journey batch: %w", err)
					}
				}
			}
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
		return datasets.DataImportReport{}, err
	}
	if dataset.SupportedObjects.Journeys {
		if err := flushJourneys(); err != nil {
			return datasets.DataImportReport{}, err
		}
	}
	if err := trackStore.Flush(); err != nil {
		return datasets.DataImportReport{}, err
	}
	if dataset.SupportedObjects.Services {
		serviceRoutesQueue := NewDatabaseBatchProcessingQueue("services_raw", 1*time.Second, 1*time.Minute, 1000)
		serviceRoutesQueue.Process()
		for _, service := range g.ctdfServices {
			if len(service.Routes) == 0 {
				continue
			}
			sort.Slice(service.Routes, func(i, j int) bool {
				if service.Routes[i].Origin != service.Routes[j].Origin {
					return service.Routes[i].Origin < service.Routes[j].Origin
				}
				return service.Routes[i].Destination < service.Routes[j].Destination
			})
			update, err := bson.Marshal(bson.M{"$set": bson.M{"routes": service.Routes}})
			if err != nil {
				return datasets.DataImportReport{}, err
			}
			model := mongo.NewUpdateOneModel().SetFilter(bson.M{"primaryidentifier": service.PrimaryIdentifier}).SetUpdate(update)
			serviceRoutesQueue.Add(model)
		}
		serviceRoutesQueue.Wait()
	}
	gtfsTrips = nil

	log.Info().Str("dataset", dataset.Identifier).
		Int("unique_tracks", trackStore.unique).
		Int("reused_tracks", trackStore.reused).
		Int("snap_cache_entries", shapeSnapCacheEntries).
		Int("snap_cache_evictions", shapeSnapCacheEvictions).
		Int("path_pattern_hits", pathPatternCacheHits).
		Int("path_pattern_misses", pathPatternCacheMisses).
		Int("path_pattern_cache_evictions", pathTrackCache.evictions).
		Msg("Finished GTFS journey track processing")

	if rejectedJourneys > 0 {
		log.Warn().Str("dataset", dataset.Identifier).Int("rejected_journeys", rejectedJourneys).Msg("GTFS journeys omitted because their times could not be resolved")
	}
	log.Info().Msg("Finished Journeys")

	return datasets.DataImportReport{
		ImportedOperators: len(g.Agencies),
		ImportedStops:     len(g.stopLocations),
		ImportedServices:  len(g.ctdfServices),
		ImportedJourneys:  importedJourneys,
	}, nil
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
	1501: ctdf.TransportTypeTaxiBus,
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
	} else if intType >= 1500 && intType <= 1507 {
		return ctdf.TransportTypeTaxi
	} else {
		return ctdf.TransportTypeUnknown
	}
}

func parseOptionalGTFSTime(timestamp string) (time.Time, error) {
	if strings.TrimSpace(timestamp) == "" {
		return time.Time{}, nil
	}
	return parseGTFSTime(timestamp)
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

func parseExactTimes(value string) (int8, error) {
	switch strings.TrimSpace(value) {
	case "", "0":
		return 0, nil
	case "1":
		return 1, nil
	default:
		return 0, fmt.Errorf("invalid exact_times value %q", value)
	}
}

func expandJourneyForFrequencies(journey *ctdf.Journey, windows []frequencyWindow) []*ctdf.Journey {
	// Performance Review [Set O] [PR042] [P2] (batch): All frequency occurrences and cloned paths
	// are materialised before the bounded writer sees the first one. Retained memory
	// scales with occurrence count times path length. Yield occurrences incrementally
	// into the writer and validate a per-template expansion budget; preserve stable
	// start-time IDs, >24h times and non-overlapping window semantics. Measure peak heap
	// with dense frequency windows as well as ordinary scheduled trips.
	if journey == nil {
		return nil
	}
	if len(windows) == 0 {
		return []*ctdf.Journey{journey}
	}

	orderedWindows := append([]frequencyWindow(nil), windows...)
	sort.SliceStable(orderedWindows, func(i, j int) bool {
		return orderedWindows[i].StartTime.Before(orderedWindows[j].StartTime)
	})

	templateDepartureTime := journey.DepartureTime
	if templateDepartureTime.IsZero() && len(journey.Calls) > 0 {
		templateDepartureTime = journey.Calls[0].Departure
	}

	expanded := make([]*ctdf.Journey, 0)
	var previousWindowEnd time.Time
	for _, window := range orderedWindows {
		if window.HeadwaySeconds <= 0 || !window.StartTime.Before(window.EndTime) {
			continue
		}
		// GTFS frequency windows for a trip must not overlap. Keep the first
		// ordered window if malformed input violates that rule; a window may
		// legitimately begin exactly when the previous one ends.
		if !previousWindowEnd.IsZero() && window.StartTime.Before(previousWindowEnd) {
			continue
		}
		previousWindowEnd = window.EndTime

		for occurrenceStart := window.StartTime; occurrenceStart.Before(window.EndTime); occurrenceStart = occurrenceStart.Add(time.Duration(window.HeadwaySeconds) * time.Second) {
			occurrence := cloneJourneyWithTimeShift(journey, occurrenceStart.Sub(templateDepartureTime))
			occurrence.PrimaryIdentifier = fmt.Sprintf("%s-frequency-%d", journey.PrimaryIdentifier, gtfsTimeSeconds(occurrenceStart))
			if occurrence.OtherIdentifiers == nil {
				occurrence.OtherIdentifiers = map[string]string{}
			}
			occurrence.OtherIdentifiers["GTFS-TripStartTime"] = formatGTFSTime(occurrenceStart)
			expanded = append(expanded, occurrence)
		}
	}

	return expanded
}

func cloneJourneyWithTimeShift(journey *ctdf.Journey, offset time.Duration) *ctdf.Journey {
	clone := *journey

	if journey.OtherIdentifiers != nil {
		clone.OtherIdentifiers = make(map[string]string, len(journey.OtherIdentifiers))
		for key, value := range journey.OtherIdentifiers {
			clone.OtherIdentifiers[key] = value
		}
	}
	if journey.Availability != nil {
		availability := *journey.Availability
		availability.Match = append([]ctdf.AvailabilityRule(nil), journey.Availability.Match...)
		availability.MatchSecondary = append([]ctdf.AvailabilityRule(nil), journey.Availability.MatchSecondary...)
		availability.Condition = append([]ctdf.AvailabilityRule(nil), journey.Availability.Condition...)
		availability.Exclude = append([]ctdf.AvailabilityRule(nil), journey.Availability.Exclude...)
		clone.Availability = &availability
	}
	if journey.Calls != nil {
		clone.Calls = make([]ctdf.JourneyCall, len(journey.Calls))
		for index, call := range journey.Calls {
			callClone := call
			if !callClone.Arrival.IsZero() {
				callClone.Arrival = callClone.Arrival.Add(offset)
			}
			if !callClone.Departure.IsZero() {
				callClone.Departure = callClone.Departure.Add(offset)
			}
			if call.IncomingArrival != nil {
				incomingArrival := *call.IncomingArrival
				if !incomingArrival.Time().IsZero() {
					incomingArrival = primitive.NewDateTimeFromTime(incomingArrival.Time().Add(offset))
				}
				callClone.IncomingArrival = &incomingArrival
			}
			if call.IncomingPlatform != nil {
				incomingPlatform := *call.IncomingPlatform
				callClone.IncomingPlatform = &incomingPlatform
			}
			if call.Outgoing != nil {
				outgoing := *call.Outgoing
				callClone.Outgoing = &outgoing
			}
			clone.Calls[index] = callClone
		}
	}
	if !journey.DepartureTime.IsZero() {
		clone.DepartureTime = journey.DepartureTime.Add(offset)
	} else if len(journey.Calls) > 0 && !journey.Calls[0].Departure.IsZero() {
		clone.DepartureTime = journey.Calls[0].Departure.Add(offset)
	}
	return &clone
}

func gtfsTimeSeconds(value time.Time) int64 {
	base := time.Date(0, time.January, 1, 0, 0, 0, 0, value.Location())
	return int64(value.Sub(base) / time.Second)
}

func formatGTFSTime(value time.Time) string {
	seconds := gtfsTimeSeconds(value)
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, (seconds%3600)/60, seconds%60)
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
