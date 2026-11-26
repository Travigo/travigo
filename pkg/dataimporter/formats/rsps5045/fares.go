package rsps5045

import (
	"archive/zip"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	fareversionlease "github.com/travigo/travigo/pkg/dataimporter/fareversion"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	flowStageCollection = "rsps5045_flow_staging"
	fareStageCollection = "rsps5045_fare_staging"
	maxArchiveEntrySize = 512 << 20
	maxArchiveSize      = 2 << 30
	maxRecordLength     = 4096
	writeBatchSize      = 500
)

// Fares reads an RSPS5045 ZIP as a linked dataset. The flow and fare staging
// collections keep the large FFL cross-reference bounded in application memory.
type Fares struct {
	archive *zip.Reader
	files   map[string]*zip.File
	mode    byte
	seq     string
}

type fileHeader struct {
	contentType string
	sequence    string
	records     int64
	generated   time.Time
}

type flowStage struct {
	RunID        string    `bson:"runid"`
	FlowID       string    `bson:"flowid"`
	Origin       string    `bson:"origin"`
	Destination  string    `bson:"destination"`
	RouteCode    string    `bson:"routecode"`
	StatusCode   string    `bson:"statuscode"`
	UsageCode    string    `bson:"usagecode"`
	Direction    string    `bson:"direction"`
	StartDate    time.Time `bson:"startdate,omitempty"`
	EndDate      time.Time `bson:"enddate,omitempty"`
	FareTOC      string    `bson:"fare_toc,omitempty"`
	ViaLondon    string    `bson:"vialondon,omitempty"`
	DiscountFlag string    `bson:"discountflag,omitempty"`
}

type fareStage struct {
	RunID           string `bson:"runid"`
	FlowID          string `bson:"flowid"`
	TicketCode      string `bson:"ticketcode"`
	AmountPence     int64  `bson:"amountpence"`
	RestrictionCode string `bson:"restrictioncode,omitempty"`
}

type flowFare struct {
	Flow flowStage `bson:"flow"`
	Fare fareStage `bson:"fare"`
}

type ticketType struct {
	Code          string
	Name          string
	Class         ctdf.FareClass
	Type          ctdf.FareProductType
	EffectiveFrom time.Time
	EffectiveTo   time.Time
	ProductRef    string
	RailTicket    ctdf.FareRailTicket
}

type ticketFlowSegment struct {
	Start          time.Time
	End            time.Time
	Product        ticketType
	OriginRef      string
	DestinationRef string
	Route          *rspsRouteRecord
}

type ticketFlowGap struct {
	Start time.Time
	End   time.Time
}

type ticketValidityRecord struct {
	Code          string
	EffectiveFrom time.Time
	EffectiveTo   time.Time
	Validity      ctdf.FareTicketValidity
}

type advancePurchaseRecord struct {
	TicketCode string
	Condition  ctdf.FareRailAdvancePurchase
}

type tocSpecificTicketRecord struct {
	TicketCode string
	Condition  ctdf.FareRailTOCTicket
}

type railcardRecord struct {
	Code          string
	Name          string
	EffectiveFrom time.Time
	EffectiveTo   time.Time
}

type locationData struct {
	UIC       string
	NLC       string
	Name      string
	CRS       string
	FareGroup string
	Start     time.Time
	End       time.Time
}

type clusterMember struct {
	Cluster string
	Member  string
	Start   time.Time
	End     time.Time
}

type clusterLocation struct {
	Ref   string
	Start time.Time
	End   time.Time
}

type clusterMembershipVersion struct {
	Start   time.Time
	End     time.Time
	Members []string
}

type rspsRouteRecord struct {
	Code      string
	Name      string
	Start     time.Time
	End       time.Time
	Locations []ctdf.FareRailRouteLocation
}

func clusterMembershipVersions(members []clusterMember) []clusterMembershipVersion {
	var boundaries []time.Time
	for _, member := range members {
		if !member.Start.IsZero() {
			boundaries = append(boundaries, member.Start)
		}
		if !member.End.IsZero() {
			boundaries = append(boundaries, member.End.AddDate(0, 0, 1))
		}
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].Before(boundaries[j]) })
	uniqueBoundaries := boundaries[:0]
	for _, boundary := range boundaries {
		if len(uniqueBoundaries) == 0 || !uniqueBoundaries[len(uniqueBoundaries)-1].Equal(boundary) {
			uniqueBoundaries = append(uniqueBoundaries, boundary)
		}
	}

	var versions []clusterMembershipVersion
	appendVersion := func(start, end time.Time, active func(clusterMember) bool) {
		membersByID := make(map[string]bool)
		for _, member := range members {
			if active(member) {
				membersByID[member.Member] = true
			}
		}
		if len(membersByID) == 0 {
			return
		}
		memberIDs := make([]string, 0, len(membersByID))
		for member := range membersByID {
			memberIDs = append(memberIDs, member)
		}
		sort.Strings(memberIDs)
		if len(versions) > 0 {
			previous := &versions[len(versions)-1]
			if previous.End.AddDate(0, 0, 1).Equal(start) && equalStrings(previous.Members, memberIDs) {
				previous.End = end
				return
			}
		}
		versions = append(versions, clusterMembershipVersion{Start: start, End: end, Members: memberIDs})
	}
	if len(uniqueBoundaries) == 0 {
		appendVersion(time.Time{}, time.Time{}, func(member clusterMember) bool {
			return member.Start.IsZero() && member.End.IsZero()
		})
		return versions
	}
	first := uniqueBoundaries[0]
	appendVersion(time.Time{}, first.AddDate(0, 0, -1), func(member clusterMember) bool {
		return member.Start.IsZero() && (member.End.IsZero() || !member.End.Before(first.AddDate(0, 0, -1)))
	})
	for index, start := range uniqueBoundaries {
		var end time.Time
		if index+1 < len(uniqueBoundaries) {
			end = uniqueBoundaries[index+1].AddDate(0, 0, -1)
		}
		appendVersion(start, end, func(member clusterMember) bool {
			return fareDateMatches(member.Start, member.End, start)
		})
	}
	return versions
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (*Fares) ParseFile(io.Reader) error {
	return errors.New("RSPS5045 fares must be parsed as a complete ZIP archive")
}

// ParseArchive validates the feed manifest and every member's common framing
// before Import writes any candidate records.
func (format *Fares) ParseArchive(reader io.ReaderAt, size int64) error {
	if reader == nil || size <= 0 {
		return errors.New("RSPS5045 fare archive is empty")
	}
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return fmt.Errorf("open RSPS5045 ZIP: %w", err)
	}
	files := make(map[string]*zip.File, len(archive.File))
	var totalUncompressed uint64
	for _, member := range archive.File {
		name := strings.ToUpper(path.Base(strings.ReplaceAll(member.Name, "\\", "/")))
		if member.FileInfo().IsDir() || name == "" {
			continue
		}
		if _, exists := files[name]; exists {
			return fmt.Errorf("RSPS5045 ZIP contains duplicate member %q", name)
		}
		if member.UncompressedSize64 > maxArchiveEntrySize {
			return fmt.Errorf("RSPS5045 member %s exceeds the %d byte limit", name, maxArchiveEntrySize)
		}
		totalUncompressed += member.UncompressedSize64
		if totalUncompressed > maxArchiveSize {
			return fmt.Errorf("RSPS5045 archive exceeds the %d byte expanded-size limit", maxArchiveSize)
		}
		files[name] = member
	}
	var manifestName string
	for name := range files {
		if strings.HasSuffix(name, ".DAT") {
			if manifestName != "" {
				return errors.New("RSPS5045 ZIP contains more than one .DAT manifest")
			}
			manifestName = name
		}
	}
	if manifestName == "" {
		return errors.New("RSPS5045 ZIP does not contain an .DAT manifest")
	}
	mode, sequence, err := parseFeedName(manifestName)
	if err != nil {
		return fmt.Errorf("RSPS5045 manifest name: %w", err)
	}
	manifestFiles, err := readManifest(files[manifestName], manifestName, sequence)
	if err != nil {
		return err
	}
	for _, listedName := range manifestFiles {
		memberName := strings.ToUpper(path.Base(strings.ReplaceAll(listedName, "\\", "/")))
		member, exists := files[memberName]
		if !exists {
			return fmt.Errorf("RSPS5045 manifest lists missing file %q", listedName)
		}
		memberMode, memberSequence, err := parseFeedName(memberName)
		if err != nil {
			return fmt.Errorf("RSPS5045 manifest member %s: %w", memberName, err)
		}
		if memberMode != mode || memberSequence != sequence {
			return fmt.Errorf("RSPS5045 manifest member %s does not match feed %c/%s", memberName, mode, sequence)
		}
		extension := strings.TrimPrefix(path.Ext(memberName), ".")
		header, _, err := scanFile(member, memberName, extension, nil, nil)
		if err != nil {
			return err
		}
		if header.sequence != memberSequence {
			return fmt.Errorf("RSPS5045 member %s sequence %s does not match filename sequence %s", memberName, header.sequence, memberSequence)
		}
	}
	format.archive = archive
	format.files = files
	format.mode = mode
	format.seq = sequence
	return nil
}

func parseFeedName(name string) (byte, string, error) {
	name = strings.ToUpper(path.Base(name))
	if len(name) != 12 || !strings.HasPrefix(name, "RJFA") || name[5] < '0' || name[5] > '9' || name[6] < '0' || name[6] > '9' || name[7] < '0' || name[7] > '9' {
		return 0, "", fmt.Errorf("unexpected feed filename %q", name)
	}
	mode := name[4]
	if mode != 'F' && mode != 'C' {
		return 0, "", fmt.Errorf("unexpected refresh marker in %q", name)
	}
	return mode, name[5:8], nil
}

func readManifest(file *zip.File, name, expectedSequence string) ([]string, error) {
	var names []string
	header, count, err := scanFile(file, name, "DAT", nil, func(record []byte, _ int64) error {
		listed := strings.TrimSpace(string(record))
		if listed == "" || len(listed) != 12 {
			return fmt.Errorf("manifest filename %q must be 12 characters", listed)
		}
		names = append(names, listed)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if header.sequence != expectedSequence {
		return nil, fmt.Errorf("manifest %s sequence %s does not match filename sequence %s", name, header.sequence, expectedSequence)
	}
	if count != int64(len(names)) {
		return nil, fmt.Errorf("manifest %s record count is inconsistent", name)
	}
	return names, nil
}

func (format *Fares) filesByExtension(extension string) []*zip.File {
	var result []*zip.File
	for name, file := range format.files {
		if strings.HasSuffix(name, extension) {
			result = append(result, file)
		}
	}
	return result
}

type recordHandler func([]byte, int64) error

func scanFile(file *zip.File, name, expectedType string, lengths map[byte]int, handle recordHandler) (fileHeader, int64, error) {
	reader, err := file.Open()
	if err != nil {
		return fileHeader{}, 0, fmt.Errorf("open RSPS5045 member %s: %w", name, err)
	}
	defer reader.Close()
	buffered := bufio.NewReader(reader)
	var header fileHeader
	var lines, records int64
	sawStart, sawEnd := false, false
	headerFields := map[string]bool{}
	for {
		line, readErr := readLine(buffered)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return header, records, fmt.Errorf("read %s: %w", name, readErr)
		}
		if len(line) == 0 && !errors.Is(readErr, io.EOF) {
			return header, records, fmt.Errorf("%s line %d is blank", name, lines+1)
		}
		if len(line) > 0 {
			lines++
			if line[0] == '/' {
				comment := strings.TrimSpace(string(line))
				switch {
				case strings.HasPrefix(comment, "/!! Start of file"):
					if sawStart || lines != 1 {
						return header, records, fmt.Errorf("%s line %d has a misplaced start marker", name, lines)
					}
					sawStart = true
				case strings.HasPrefix(comment, "/!! Content type:"):
					if !sawStart || headerFields["type"] {
						return header, records, fmt.Errorf("%s line %d has an invalid content type header", name, lines)
					}
					header.contentType = strings.TrimSpace(strings.TrimPrefix(comment, "/!! Content type:"))
					headerFields["type"] = true
				case strings.HasPrefix(comment, "/!! Sequence:"):
					if !sawStart || headerFields["sequence"] {
						return header, records, fmt.Errorf("%s line %d has an invalid sequence header", name, lines)
					}
					header.sequence = strings.TrimSpace(strings.TrimPrefix(comment, "/!! Sequence:"))
					headerFields["sequence"] = true
				case strings.HasPrefix(comment, "/!! Records:"):
					if !sawStart || headerFields["records"] {
						return header, records, fmt.Errorf("%s line %d has an invalid record count header", name, lines)
					}
					header.records, err = strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(comment, "/!! Records:")), 10, 64)
					if err != nil || header.records < 0 {
						return header, records, fmt.Errorf("%s line %d has an invalid record count", name, lines)
					}
					headerFields["records"] = true
				case strings.HasPrefix(comment, "/!! Generated:"):
					if !sawStart || headerFields["generated"] {
						return header, records, fmt.Errorf("%s line %d has an invalid generated date header", name, lines)
					}
					value := strings.TrimSpace(strings.TrimPrefix(comment, "/!! Generated:"))
					header.generated, err = time.Parse("02/01/2006", value)
					if err != nil {
						return header, records, fmt.Errorf("%s line %d has an invalid generated date %q", name, lines, value)
					}
					headerFields["generated"] = true
				case strings.HasPrefix(comment, "/!! Exporter:"):
					if !sawStart || headerFields["exporter"] {
						return header, records, fmt.Errorf("%s line %d has an invalid exporter header", name, lines)
					}
					headerFields["exporter"] = true
				case strings.HasPrefix(comment, "/!! End of file"):
					if !sawStart || sawEnd {
						return header, records, fmt.Errorf("%s line %d has an invalid end marker", name, lines)
					}
					dateText := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(comment, "/!! End of file"), ")"))
					dateText = strings.TrimPrefix(dateText, "(")
					if _, err := time.Parse("02/01/2006", dateText); err != nil {
						return header, records, fmt.Errorf("%s line %d has an invalid end date %q", name, lines, dateText)
					}
					sawEnd = true
				}
				if sawEnd && len(headerFields) != 5 {
					return header, records, fmt.Errorf("%s ended before all required headers were read", name)
				}
			} else {
				if !sawStart || sawEnd || len(headerFields) != 5 {
					return header, records, fmt.Errorf("%s line %d contains a record outside the data section", name, lines)
				}
				if len(line) > maxRecordLength {
					return header, records, fmt.Errorf("%s line %d exceeds the %d character record limit", name, lines, maxRecordLength)
				}
				if expectedLength, strict := expectedRecordLength(lengths, line); strict && len(line) != expectedLength {
					return header, records, fmt.Errorf("%s line %d has length %d; expected %d for record type %q", name, lines, len(line), expectedLength, recordType(line))
				}
				records++
				if handle != nil {
					if err := handle(line, lines); err != nil {
						return header, records, fmt.Errorf("%s line %d: %w", name, lines, err)
					}
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	if !sawStart || !sawEnd {
		return header, records, fmt.Errorf("%s is missing a start or end marker", name)
	}
	if !headerFields["type"] || !headerFields["sequence"] || !headerFields["records"] || !headerFields["generated"] || !headerFields["exporter"] {
		return header, records, fmt.Errorf("%s is missing a required header", name)
	}
	if expectedType != "" && header.contentType != expectedType {
		return header, records, fmt.Errorf("%s content type is %q; expected %q", name, header.contentType, expectedType)
	}
	if records != header.records {
		return header, records, fmt.Errorf("%s header declares %d records but contains %d", name, header.records, records)
	}
	return header, records, nil
}

func readLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return nil, fmt.Errorf("line exceeds the %d byte input buffer", reader.Size())
	}
	if len(line) > maxRecordLength+2 {
		return nil, fmt.Errorf("line exceeds %d bytes", maxRecordLength+2)
	}
	trimmed := strings.TrimSuffix(string(line), "\n")
	trimmed = strings.TrimSuffix(trimmed, "\r")
	return []byte(trimmed), err
}

func expectedRecordLength(lengths map[byte]int, record []byte) (int, bool) {
	if lengths == nil {
		return 0, false
	}
	expected, exists := lengths[recordType(record)]
	return expected, exists
}

func recordType(record []byte) byte {
	if len(record) < 2 {
		return 0
	}
	return record[1]
}

func dateField(value []byte) (time.Time, error) {
	text := strings.TrimSpace(string(value))
	if text == "" {
		return time.Time{}, nil
	}
	date, err := time.ParseInLocation("02012006", text, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid ddmmyyyy date %q", text)
	}
	return date, nil
}

type fareRecordFactory func(kind, sourceID string, from, to time.Time) ctdf.FareRecord

func (format *Fares) Import(dataset datasets.DataSet, datasource *ctdf.DataSourceReference) (datasets.DataImportReport, error) {
	if format.archive == nil || format.files == nil {
		return datasets.DataImportReport{}, errors.New("RSPS5045 archive has not been parsed")
	}
	if datasource == nil || datasource.FareVersionID == "" {
		return datasets.DataImportReport{}, errors.New("RSPS5045 fare import requires an immutable candidate version")
	}
	ctx := datasource.ImportContext
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return datasets.DataImportReport{}, err
	}
	cleanupArchive, err := format.prepareSourceArchive(ctx, dataset.Identifier, datasource.FareVersionID, datasource)
	if err != nil {
		return datasets.DataImportReport{}, err
	}
	defer cleanupArchive()
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = database.GetCollection(flowStageCollection).DeleteMany(cleanupCtx, bson.M{"runid": datasource.FareVersionID})
		_, _ = database.GetCollection(fareStageCollection).DeleteMany(cleanupCtx, bson.M{"runid": datasource.FareVersionID})
	}()
	datasource.FareSourceSequence = format.seq
	datasource.OriginalFormat = string(datasets.DataSetFormatNationalRailFares)
	record := func(kind, sourceID string, from, to time.Time) ctdf.FareRecord {
		return ctdf.FareRecord{
			PrimaryIdentifier: fmt.Sprintf("%s-rsps5045-%s-%s", dataset.Identifier, kind, sourceID),
			DatasetID:         dataset.Identifier,
			FareVersionID:     datasource.FareVersionID,
			SourceIdentifier:  sourceID,
			DataSource:        datasource,
			EffectiveFrom:     from,
			EffectiveTo:       to,
			CreationDateTime:  time.Now(),
		}
	}
	report := datasets.DataImportReport{FareDataPresent: true}
	adultRef := fmt.Sprintf("%s-rsps5045-rider-adult", dataset.Identifier)
	if err := upsertFare("fare_rider_categories", adultRef, &ctdf.FareRiderCategory{
		FareRecord: setRecordPrimary(record("rider", "adult", time.Time{}, time.Time{}), adultRef),
		Name:       "Adult", UserType: "adult", IsDefault: true,
	}); err != nil {
		return report, err
	}
	report.ImportedFareRiderCategories++
	_, railcardCount, err := format.importRailcards(dataset, record)
	if err != nil {
		return report, err
	}
	report.ImportedFareRiderCategories += railcardCount
	railRestrictionCatalog, err := format.importRailRestrictions()
	if err != nil {
		return report, err
	}
	advancePurchase, err := format.importAdvancePurchase()
	if err != nil {
		return report, err
	}
	tocSpecificTickets, err := format.importTOCSpecificTickets()
	if err != nil {
		return report, err
	}
	tickets, ticketCount, err := format.importTicketTypes(dataset, record, adultRef, railRestrictionCatalog.calendars)
	if err != nil {
		return report, err
	}
	report.ImportedFareProducts = ticketCount
	nlcRefs, clusterRefs, locationCount, linkedLocations, unlinkedLocations, err := format.importLocations(dataset, record)
	if err != nil {
		return report, err
	}
	report.ImportedFareLocations += locationCount
	report.FareLocationsLinkedToStops = linkedLocations
	report.FareLocationsWithoutStopLink = unlinkedLocations
	routes, err := format.importRoutes()
	if err != nil {
		return report, err
	}
	fareTOCRefs, err := format.readFareTOCs()
	if err != nil {
		return report, err
	}
	flowFiles := format.filesByExtension(".FFL")
	if len(flowFiles) != 1 {
		return report, fmt.Errorf("RSPS5045 full feed must contain exactly one FFL file, found %d", len(flowFiles))
	}
	if err := format.stageFlowsAndFares(ctx, flowFiles[0], datasource); err != nil {
		return report, err
	}
	if err := format.writeFlowFares(dataset, datasource, record, tickets, adultRef, nlcRefs, clusterRefs, routes, fareTOCRefs, railRestrictionCatalog.restrictions, advancePurchase, tocSpecificTickets, &report); err != nil {
		return report, err
	}
	return report, nil
}

func (format *Fares) importTicketTypes(dataset datasets.DataSet, record fareRecordFactory, adultRef string, calendars map[string][]ctdf.FareRailCalendar) (map[string][]ticketType, int, error) {
	files := format.filesByExtension(".TTY")
	if len(files) != 1 {
		return nil, 0, fmt.Errorf("RSPS5045 full feed must contain exactly one TTY file, found %d", len(files))
	}
	tickets := map[string][]ticketType{}
	validities, err := format.importTicketValidities()
	if err != nil {
		return nil, 0, err
	}
	count := 0
	_, _, err = scanFile(files[0], files[0].Name, "TTY", nil, func(line []byte, _ int64) error {
		if len(line) != 113 {
			return fmt.Errorf("ticket type record has length %d; expected 113", len(line))
		}
		if line[0] != 'R' {
			return fmt.Errorf("ticket type update marker %q is not valid in a full refresh", line[0])
		}
		code := strings.TrimSpace(string(line[1:4]))
		name := strings.TrimSpace(string(line[28:43]))
		if code == "" || name == "" {
			return errors.New("ticket type is missing its code or description")
		}
		from, err := dateField(line[12:20])
		if err != nil {
			return err
		}
		to, err := dateField(line[4:12])
		if err != nil {
			return err
		}
		productType := ctdf.FareProductTypeOther
		switch line[44] {
		case 'S':
			productType = ctdf.FareProductTypeSingleRide
		case 'R':
			productType = ctdf.FareProductTypeReturn
		case 'N':
			productType = ctdf.FareProductTypeSeason
		default:
			return fmt.Errorf("ticket %s has unsupported type %q", code, line[44])
		}
		class := ctdf.FareClassOther
		switch line[43] {
		case '1':
			class = ctdf.FareClassFirst
		case '2':
			class = ctdf.FareClassStandard
		case '9':
			class = ctdf.FareClassOther
		default:
			return fmt.Errorf("ticket %s has unsupported class %q", code, line[43])
		}
		idSuffix := fmt.Sprintf("%s-%s-%s", code, dateKey(from), dateKey(to))
		productRef := fmt.Sprintf("%s-rsps5045-product-%s", dataset.Identifier, idSuffix)
		lastValidDay, err := dateField(line[46:54])
		if err != nil {
			return err
		}
		minPassengers, err := parseRSPSInt(line[57:60], "TTY MIN_PASSENGERS")
		if err != nil {
			return err
		}
		maxPassengers, err := parseRSPSInt(line[54:57], "TTY MAX_PASSENGERS")
		if err != nil {
			return err
		}
		railTicket := ctdf.FareRailTicket{
			TicketCode:        code,
			ValidityCode:      strings.TrimSpace(string(line[75:77])),
			DiscountCategory:  strings.TrimSpace(string(line[111:113])),
			LastValidDay:      lastValidDay,
			Reservation:       strings.TrimSpace(string(line[98:99])),
			MinimumPassengers: minPassengers,
			MaximumPassengers: maxPassengers,
			Calendars:         calendars[code],
		}
		if validity, ok := chooseTicketValidity(validities, railTicket.ValidityCode, from); ok {
			validityCopy := validity
			railTicket.Validity = &validityCopy
		}
		product := &ctdf.FareProduct{
			FareRecord: setRecordPrimary(record("product", idSuffix, from, to), productRef),
			Name:       name, Type: productType, Class: class, RiderRef: adultRef, RailTicket: &railTicket,
		}
		if err := upsertFare("fare_products", productRef, product); err != nil {
			return err
		}
		tickets[code] = append(tickets[code], ticketType{Code: code, Name: name, Class: class, Type: productType, EffectiveFrom: from, EffectiveTo: to, ProductRef: productRef, RailTicket: railTicket})
		count++
		return nil
	})
	return tickets, count, err
}

func parseRSPSInt(value []byte, field string) (int, error) {
	text := strings.TrimSpace(string(value))
	if text == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(text)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid %s value %q", field, text)
	}
	return parsed, nil
}

func (format *Fares) importTicketValidities() (map[string][]ticketValidityRecord, error) {
	files := format.filesByExtension(".TVL")
	if len(files) == 0 {
		return map[string][]ticketValidityRecord{}, nil
	}
	if len(files) != 1 {
		return nil, fmt.Errorf("RSPS5045 feed has %d TVL files", len(files))
	}
	validities := map[string][]ticketValidityRecord{}
	_, _, err := scanFile(files[0], files[0].Name, "TVL", nil, func(line []byte, _ int64) error {
		if len(line) != 82 {
			return fmt.Errorf("TVL record has length %d; expected 82", len(line))
		}
		code := strings.TrimSpace(string(line[0:2]))
		if code == "" {
			return errors.New("TVL record has no validity code")
		}
		end, err := dateField(line[2:10])
		if err != nil {
			return err
		}
		from, err := dateField(line[10:18])
		if err != nil {
			return err
		}
		values := make([]int, 5)
		for index, field := range [][]byte{line[38:40], line[40:42], line[42:44], line[44:46], line[46:48]} {
			values[index], err = parseRSPSInt(field, "TVL numeric field")
			if err != nil {
				return err
			}
		}
		returnAfterMonths, err := parseRSPSInt(line[48:50], "TVL return-after months")
		if err != nil {
			return err
		}
		validity := ctdf.FareTicketValidity{
			Description:         strings.TrimSpace(string(line[18:38])),
			OutwardDays:         values[0],
			OutwardMonths:       values[1],
			ReturnDays:          values[2],
			ReturnMonths:        values[3],
			ReturnAfterDays:     values[4],
			ReturnAfterMonths:   returnAfterMonths,
			ReturnAfterWeekday:  strings.TrimSpace(string(line[50:52])),
			BreakOutwardAllowed: strings.EqualFold(strings.TrimSpace(string(line[52:53])), "Y"),
			BreakReturnAllowed:  strings.EqualFold(strings.TrimSpace(string(line[53:54])), "Y"),
			OutwardDescription:  strings.TrimSpace(string(line[54:68])),
			ReturnDescription:   strings.TrimSpace(string(line[68:82])),
		}
		if !from.IsZero() && !end.IsZero() && end.Before(from) {
			return fmt.Errorf("TVL validity %s ends before it starts", code)
		}
		validities[code] = append(validities[code], ticketValidityRecord{Code: code, EffectiveFrom: from, EffectiveTo: end, Validity: validity})
		return nil
	})
	return validities, err
}

func chooseTicketValidity(validities map[string][]ticketValidityRecord, code string, onDate time.Time) (ctdf.FareTicketValidity, bool) {
	for _, validity := range validities[code] {
		if fareDateMatches(validity.EffectiveFrom, validity.EffectiveTo, onDate) {
			return validity.Validity, true
		}
	}
	if len(validities[code]) == 1 {
		return validities[code][0].Validity, true
	}
	return ctdf.FareTicketValidity{}, false
}

func (format *Fares) importAdvancePurchase() (map[string][]advancePurchaseRecord, error) {
	files := format.filesByExtension(".TAP")
	if len(files) == 0 {
		return map[string][]advancePurchaseRecord{}, nil
	}
	if len(files) != 1 {
		return nil, fmt.Errorf("RSPS5045 feed has %d TAP files", len(files))
	}
	result := map[string][]advancePurchaseRecord{}
	_, _, err := scanFile(files[0], files[0].Name, "TAP", nil, func(line []byte, _ int64) error {
		if len(line) != 37 {
			return fmt.Errorf("TAP record has length %d; expected 37", len(line))
		}
		ticketCode := strings.TrimSpace(string(line[0:3]))
		restrictionCode := strings.TrimSpace(string(line[3:5]))
		restrictionFlag := strings.TrimSpace(string(line[5:6]))
		if ticketCode == "" || (restrictionFlag != "0" && restrictionFlag != "1" && restrictionFlag != "2") {
			return fmt.Errorf("TAP record has invalid ticket or restriction flag %q/%q", ticketCode, restrictionFlag)
		}
		validTo, err := dateField(line[8:16])
		if err != nil {
			return err
		}
		validFrom, err := dateField(line[16:24])
		if err != nil {
			return err
		}
		if !validFrom.IsZero() && !validTo.IsZero() && validTo.Before(validFrom) {
			return fmt.Errorf("TAP ticket %s ends before it starts", ticketCode)
		}
		checkType := strings.TrimSpace(string(line[24:25]))
		data := strings.TrimSpace(string(line[25:33]))
		condition := ctdf.FareRailAdvancePurchase{
			RestrictionCode: restrictionCode,
			RestrictionFlag: restrictionFlag,
			TOCCode:         strings.TrimSpace(string(line[6:8])),
			ValidFrom:       validFrom,
			ValidTo:         validTo,
			CheckType:       checkType,
		}
		switch checkType {
		case "0":
			condition.BookingDate, err = dateField(line[25:33])
			if err != nil || condition.BookingDate.IsZero() {
				return fmt.Errorf("TAP ticket %s has invalid booking date %q", ticketCode, data)
			}
		case "1":
			condition.AdvanceHours, err = parseRSPSInt(line[25:33], "TAP advance hours")
			if err != nil || condition.AdvanceHours == 0 {
				return fmt.Errorf("TAP ticket %s has invalid advance hours %q", ticketCode, data)
			}
		case "2":
			condition.AdvanceDays, err = parseRSPSInt(line[25:33], "TAP advance days")
			if err != nil || condition.AdvanceDays == 0 {
				return fmt.Errorf("TAP ticket %s has invalid advance days %q", ticketCode, data)
			}
		default:
			return fmt.Errorf("TAP ticket %s has unsupported check type %q", ticketCode, checkType)
		}
		bookingTime := strings.TrimSpace(string(line[33:37]))
		if checkType == "1" && bookingTime != "" {
			return fmt.Errorf("TAP ticket %s advance-hour record unexpectedly has a booking time", ticketCode)
		}
		if bookingTime != "" {
			condition.BookingTime, err = parseRailClock(bookingTime)
			if err != nil {
				return fmt.Errorf("TAP ticket %s booking time: %w", ticketCode, err)
			}
			condition.HasBookingTime = true
		}
		result[ticketCode] = append(result[ticketCode], advancePurchaseRecord{TicketCode: ticketCode, Condition: condition})
		return nil
	})
	return result, err
}

func (format *Fares) importTOCSpecificTickets() (map[string][]tocSpecificTicketRecord, error) {
	files := format.filesByExtension(".TSP")
	if len(files) == 0 {
		return map[string][]tocSpecificTicketRecord{}, nil
	}
	if len(files) != 1 {
		return nil, fmt.Errorf("RSPS5045 feed has %d TSP files", len(files))
	}
	result := map[string][]tocSpecificTicketRecord{}
	_, _, err := scanFile(files[0], files[0].Name, "TSP", nil, func(line []byte, _ int64) error {
		if len(line) != 68 {
			return fmt.Errorf("TSP record has length %d; expected 68", len(line))
		}
		ticketCode := strings.TrimSpace(string(line[0:3]))
		restrictionCode := strings.TrimSpace(string(line[3:5]))
		restrictionFlag := strings.TrimSpace(string(line[5:6]))
		direction := strings.TrimSpace(string(line[6:7]))
		tocCode := strings.TrimSpace(string(line[7:9]))
		tocType := strings.TrimSpace(string(line[9:10]))
		if ticketCode == "" || (restrictionFlag != "0" && restrictionFlag != "1" && restrictionFlag != "2") {
			return fmt.Errorf("TSP record has invalid ticket or restriction flag %q/%q", ticketCode, restrictionFlag)
		}
		if direction != "O" && direction != "R" && direction != "B" {
			return fmt.Errorf("TSP ticket %s has unsupported direction %q", ticketCode, direction)
		}
		if (tocType != "M" && tocType != "C") || (tocType == "M" && tocCode == "") {
			return fmt.Errorf("TSP ticket %s has invalid TOC type/code %q/%q", ticketCode, tocType, tocCode)
		}
		validTo, err := dateField(line[10:18])
		if err != nil {
			return err
		}
		validFrom, err := dateField(line[18:26])
		if err != nil {
			return err
		}
		if !validFrom.IsZero() && !validTo.IsZero() && validTo.Before(validFrom) {
			return fmt.Errorf("TSP ticket %s ends before it starts", ticketCode)
		}
		stockIndicator := strings.TrimSpace(string(line[27:28]))
		if stockIndicator != "I" && stockIndicator != "E" {
			return fmt.Errorf("TSP ticket %s has invalid stock indicator %q", ticketCode, stockIndicator)
		}
		stockList := strings.Split(strings.TrimSpace(string(line[28:68])), ",")
		if len(stockList) == 1 && stockList[0] == "" {
			stockList = nil
		} else {
			for index := range stockList {
				stockList[index] = strings.TrimSpace(stockList[index])
			}
		}
		result[ticketCode] = append(result[ticketCode], tocSpecificTicketRecord{TicketCode: ticketCode, Condition: ctdf.FareRailTOCTicket{
			RestrictionCode: restrictionCode,
			RestrictionFlag: restrictionFlag,
			Direction:       direction,
			TOCCode:         tocCode,
			TOCType:         tocType,
			ValidFrom:       validFrom,
			ValidTo:         validTo,
			SleeperRequired: strings.EqualFold(strings.TrimSpace(string(line[26:27])), "Y"),
			StockIndicator:  stockIndicator,
			StockList:       stockList,
		}})
		return nil
	})
	return result, err
}

func matchingAdvancePurchase(records []advancePurchaseRecord, restrictionCode string) []ctdf.FareRailAdvancePurchase {
	result := make([]ctdf.FareRailAdvancePurchase, 0, len(records))
	for _, record := range records {
		condition := record.Condition
		if !rspsRestrictionFlagApplies(condition.RestrictionFlag, condition.RestrictionCode, restrictionCode) {
			continue
		}
		result = append(result, condition)
	}
	return result
}

func matchingTOCSpecificTickets(records []tocSpecificTicketRecord, restrictionCode string) []ctdf.FareRailTOCTicket {
	result := make([]ctdf.FareRailTOCTicket, 0, len(records))
	for _, record := range records {
		condition := record.Condition
		if !rspsRestrictionFlagApplies(condition.RestrictionFlag, condition.RestrictionCode, restrictionCode) {
			continue
		}
		result = append(result, condition)
	}
	return result
}

func rspsRestrictionFlagApplies(flag, recordRestriction, fareRestriction string) bool {
	switch flag {
	case "0":
		return recordRestriction == fareRestriction
	case "1":
		return fareRestriction == ""
	case "2":
		return true
	default:
		return false
	}
}

func (format *Fares) importRailcards(dataset datasets.DataSet, record fareRecordFactory) ([]railcardRecord, int, error) {
	files := format.filesByExtension(".RLC")
	if len(files) == 0 {
		return nil, 0, nil
	}
	if len(files) != 1 {
		return nil, 0, fmt.Errorf("RSPS5045 feed has %d RLC files", len(files))
	}
	railcards := make([]railcardRecord, 0)
	count := 0
	_, _, err := scanFile(files[0], files[0].Name, "RLC", nil, func(line []byte, _ int64) error {
		if len(line) != 127 {
			return fmt.Errorf("RLC record has length %d; expected 127", len(line))
		}
		code := strings.TrimSpace(string(line[0:3]))
		if code == "" {
			return nil // The blank code denotes the no-railcard status record.
		}
		name := strings.TrimSpace(string(line[28:48]))
		if name == "" {
			return fmt.Errorf("RLC railcard %s has no description", code)
		}
		end, err := dateField(line[3:11])
		if err != nil {
			return err
		}
		from, err := dateField(line[11:19])
		if err != nil {
			return err
		}
		if !from.IsZero() && !end.IsZero() && end.Before(from) {
			return fmt.Errorf("RLC railcard %s ends before it starts", code)
		}
		sourceID := strings.Join([]string{code, dateKey(from), dateKey(end)}, "-")
		ref := fmt.Sprintf("%s-rsps5045-rider-%s", dataset.Identifier, safeID(sourceID))
		category := &ctdf.FareRiderCategory{
			FareRecord:      setRecordPrimary(record("rider", sourceID, from, end), ref),
			Name:            name,
			UserType:        "railcard",
			RailcardCode:    code,
			AdultStatusCode: strings.TrimSpace(string(line[118:121])),
			ChildStatusCode: strings.TrimSpace(string(line[121:124])),
			ProofRequired:   "Valid " + name,
		}
		if err := upsertFare("fare_rider_categories", ref, category); err != nil {
			return err
		}
		railcards = append(railcards, railcardRecord{Code: code, Name: name, EffectiveFrom: from, EffectiveTo: end})
		count++
		return nil
	})
	return railcards, count, err
}

func dateKey(value time.Time) string {
	if value.IsZero() {
		return "na"
	}
	return value.UTC().Format("20060102")
}

func fareDateMatches(from, to, candidate time.Time) bool {
	if candidate.IsZero() {
		return from.IsZero() && to.IsZero()
	}
	return (from.IsZero() || !candidate.Before(from)) && (to.IsZero() || !candidate.After(to))
}

func chooseTicketOnDate(choices []ticketType, onDate time.Time) (ticketType, bool) {
	var selected ticketType
	found := false
	for _, choice := range choices {
		if !fareDateMatches(choice.EffectiveFrom, choice.EffectiveTo, onDate) {
			continue
		}
		if !found || choice.EffectiveFrom.After(selected.EffectiveFrom) ||
			(choice.EffectiveFrom.Equal(selected.EffectiveFrom) && selected.EffectiveTo.After(choice.EffectiveTo)) {
			selected = choice
			found = true
		}
	}
	return selected, found
}

func ticketSegmentsForFlow(tickets map[string][]ticketType, code string, flowStart, flowEnd time.Time) ([]ticketFlowSegment, []ticketFlowGap, error) {
	choices := tickets[code]
	if len(choices) == 0 {
		return nil, []ticketFlowGap{{Start: flowStart, End: flowEnd}}, nil
	}
	if flowStart.IsZero() || flowEnd.IsZero() {
		if len(choices) == 1 {
			return []ticketFlowSegment{{Start: flowStart, End: flowEnd, Product: choices[0]}}, nil, nil
		}
		return nil, []ticketFlowGap{{Start: flowStart, End: flowEnd}}, nil
	}
	start := time.Date(flowStart.Year(), flowStart.Month(), flowStart.Day(), 0, 0, 0, 0, time.UTC)
	end := time.Date(flowEnd.Year(), flowEnd.Month(), flowEnd.Day(), 0, 0, 0, 0, time.UTC)
	if end.Before(start) {
		return nil, nil, fmt.Errorf("flow ends before it starts")
	}
	endExclusive := end.AddDate(0, 0, 1)
	boundaries := []time.Time{start, endExclusive}
	for _, choice := range choices {
		if !choice.EffectiveFrom.IsZero() && choice.EffectiveFrom.After(start) && choice.EffectiveFrom.Before(endExclusive) {
			boundaries = append(boundaries, choice.EffectiveFrom)
		}
		if !choice.EffectiveTo.IsZero() {
			nextDay := choice.EffectiveTo.AddDate(0, 0, 1)
			if nextDay.After(start) && nextDay.Before(endExclusive) {
				boundaries = append(boundaries, nextDay)
			}
		}
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].Before(boundaries[j]) })
	uniqueBoundaries := boundaries[:0]
	for _, boundary := range boundaries {
		if len(uniqueBoundaries) == 0 || !uniqueBoundaries[len(uniqueBoundaries)-1].Equal(boundary) {
			uniqueBoundaries = append(uniqueBoundaries, boundary)
		}
	}
	segments := make([]ticketFlowSegment, 0, len(uniqueBoundaries)-1)
	gaps := make([]ticketFlowGap, 0)
	for index := 0; index+1 < len(uniqueBoundaries); index++ {
		segmentStart := uniqueBoundaries[index]
		segmentEnd := uniqueBoundaries[index+1].AddDate(0, 0, -1)
		product, exists := chooseTicketOnDate(choices, segmentStart)
		if !exists {
			if len(gaps) > 0 && gaps[len(gaps)-1].End.AddDate(0, 0, 1).Equal(segmentStart) {
				gaps[len(gaps)-1].End = segmentEnd
			} else {
				gaps = append(gaps, ticketFlowGap{Start: segmentStart, End: segmentEnd})
			}
			continue
		}
		if len(segments) > 0 && segments[len(segments)-1].Product.ProductRef == product.ProductRef {
			segments[len(segments)-1].End = segmentEnd
			continue
		}
		segments = append(segments, ticketFlowSegment{Start: segmentStart, End: segmentEnd, Product: product})
	}
	return segments, gaps, nil
}

func fareDateLabel(value time.Time) string {
	if value.IsZero() {
		return "open date"
	}
	return value.Format("2006-01-02")
}

func (format *Fares) importRoutes() (map[string][]rspsRouteRecord, error) {
	files := format.filesByExtension(".RTE")
	if len(files) != 1 {
		return nil, fmt.Errorf("RSPS5045 full feed must contain exactly one RTE file, found %d", len(files))
	}
	type routeKey struct{ code, end string }
	routesByKey := map[routeKey]*rspsRouteRecord{}
	locationsByKey := map[routeKey][]ctdf.FareRailRouteLocation{}
	_, _, err := scanFile(files[0], files[0].Name, "RTE", map[byte]int{'R': 263, 'L': 26}, func(line []byte, _ int64) error {
		if len(line) < 2 {
			return errors.New("route record is shorter than its update marker and type")
		}
		if line[0] != 'R' {
			return fmt.Errorf("route update marker %q is not valid in a full refresh", line[0])
		}
		switch line[1] {
		case 'R':
			code := strings.TrimSpace(string(line[2:7]))
			if code == "" {
				return errors.New("route record has no route code")
			}
			endText := strings.TrimSpace(string(line[7:15]))
			start, err := dateField(line[15:23])
			if err != nil {
				return err
			}
			end, err := dateField([]byte(endText))
			if err != nil {
				return err
			}
			if !start.IsZero() && !end.IsZero() && end.Before(start) {
				return fmt.Errorf("route %s ends before it starts", code)
			}
			key := routeKey{code: code, end: endText}
			route := &rspsRouteRecord{Code: code, Name: strings.TrimSpace(string(line[31:47])), Start: start, End: end}
			route.Locations = append(route.Locations, locationsByKey[key]...)
			routesByKey[key] = route
		case 'L':
			code := strings.TrimSpace(string(line[2:7]))
			endText := strings.TrimSpace(string(line[7:15]))
			location := ctdf.FareRailRouteLocation{
				NLC: strings.TrimSpace(string(line[18:22])), CRS: strings.ToUpper(strings.TrimSpace(string(line[22:25]))),
				IncludeExclude: strings.TrimSpace(string(line[25:26])),
			}
			if code == "" || (location.NLC == "" && location.CRS == "") {
				return errors.New("route location record has no route or location code")
			}
			if location.IncludeExclude != "I" && location.IncludeExclude != "E" {
				return fmt.Errorf("route %s has unsupported location marker %q", code, location.IncludeExclude)
			}
			key := routeKey{code: code, end: endText}
			locationsByKey[key] = append(locationsByKey[key], location)
			if route := routesByKey[key]; route != nil {
				route.Locations = append(route.Locations, location)
			}
		default:
			return fmt.Errorf("unsupported RTE record type %q", line[1])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	routes := make(map[string][]rspsRouteRecord)
	for key, route := range routesByKey {
		if len(route.Locations) == 0 {
			route.Locations = append(route.Locations, locationsByKey[key]...)
		}
		routes[key.code] = append(routes[key.code], *route)
	}
	return routes, nil
}

func (format *Fares) readFareTOCs() (map[string]string, error) {
	files := format.filesByExtension(".TOC")
	refs := map[string]string{}
	if len(files) == 0 {
		return refs, nil
	}
	if len(files) != 1 {
		return nil, fmt.Errorf("RSPS5045 full feed has %d TOC files", len(files))
	}
	tocNames := map[string]string{}
	_, _, err := scanFile(files[0], files[0].Name, "TOC", nil, func(line []byte, _ int64) error {
		switch line[0] {
		case 'T':
			if len(line) != 42 {
				return fmt.Errorf("TOC record has length %d; expected 42", len(line))
			}
			tocID := strings.TrimSpace(string(line[1:3]))
			if tocID != "" {
				tocNames[tocID] = strings.TrimSpace(string(line[3:33]))
			}
		case 'F':
			if len(line) != 36 {
				return fmt.Errorf("Fare TOC record has length %d; expected 36", len(line))
			}
			fareID := strings.TrimSpace(string(line[1:4]))
			carrier := strings.TrimSpace(string(line[4:6]))
			name := strings.TrimSpace(string(line[6:36]))
			if name == "" {
				name = tocNames[carrier]
			}
			if fareID != "" {
				refs[fareID] = name
			}
		default:
			return fmt.Errorf("unsupported TOC record type %q", line[0])
		}
		return nil
	})
	return refs, err
}

func (format *Fares) importLocations(dataset datasets.DataSet, record fareRecordFactory) (map[string]string, map[string][]clusterLocation, int, int, int, error) {
	locFiles := format.filesByExtension(".LOC")
	clusterFiles := format.filesByExtension(".FSC")
	if len(locFiles) != 1 || len(clusterFiles) != 1 {
		return nil, nil, 0, 0, 0, fmt.Errorf("RSPS5045 full feed requires one LOC and one FSC file (found %d and %d)", len(locFiles), len(clusterFiles))
	}
	locations := map[string]locationData{}
	groupNames := map[string]string{}
	groupMembers := map[string][]string{}
	_, _, err := scanFile(locFiles[0], locFiles[0].Name, "LOC", map[byte]int{'L': 289, 'R': 20, 'G': 54, 'M': 27, 'S': 41}, func(line []byte, _ int64) error {
		if len(line) < 2 {
			return errors.New("location record is shorter than its update and type markers")
		}
		if line[0] != 'R' {
			return fmt.Errorf("location update marker %q is not valid in a full refresh", line[0])
		}
		switch line[1] {
		case 'L':
			nlc := normalizeNLC(string(line[36:40]))
			if nlc == "" {
				return nil
			}
			from, err := dateField(line[17:25])
			if err != nil {
				return err
			}
			to, err := dateField(line[9:17])
			if err != nil {
				return err
			}
			location := locationData{UIC: strings.TrimSpace(string(line[2:9])), NLC: nlc, Name: strings.TrimSpace(string(line[40:56])), CRS: strings.ToUpper(strings.TrimSpace(string(line[56:59]))), FareGroup: normalizeNLC(string(line[69:75])), Start: from, End: to}
			previous, exists := locations[nlc]
			if !exists || location.Start.After(previous.Start) {
				locations[nlc] = location
			}
		case 'G':
			uic := strings.TrimSpace(string(line[2:9]))
			if uic != "" {
				groupNames[uic] = strings.TrimSpace(string(line[33:49]))
			}
		case 'M':
			group := strings.TrimSpace(string(line[2:9]))
			member := strings.TrimSpace(string(line[17:24]))
			if group != "" && member != "" {
				groupMembers[group] = append(groupMembers[group], member)
			}
		case 'R', 'S':
		default:
			return fmt.Errorf("unsupported LOC record type %q", line[1])
		}
		return nil
	})
	if err != nil {
		return nil, nil, 0, 0, 0, err
	}

	clusterGroups := map[string][]clusterMember{}
	_, _, err = scanFile(clusterFiles[0], clusterFiles[0].Name, "FSC", nil, func(line []byte, _ int64) error {
		if len(line) != 25 {
			return fmt.Errorf("station cluster record has length %d; expected 25", len(line))
		}
		if line[0] != 'R' {
			return fmt.Errorf("station cluster update marker %q is not valid in a full refresh", line[0])
		}
		cluster := normalizeNLC(string(line[1:5]))
		member := normalizeNLC(string(line[5:9]))
		if cluster == "" || member == "" {
			return errors.New("station cluster record is missing cluster or member NLC")
		}
		end, err := dateField(line[9:17])
		if err != nil {
			return err
		}
		start, err := dateField(line[17:25])
		if err != nil {
			return err
		}
		clusterGroups[cluster] = append(clusterGroups[cluster], clusterMember{Cluster: cluster, Member: member, Start: start, End: end})
		return nil
	})
	if err != nil {
		return nil, nil, 0, 0, 0, err
	}

	codes := make([]string, 0, len(locations))
	crsByNLC := make(map[string]string, len(locations))
	for code := range locations {
		codes = append(codes, code)
		if locations[code].CRS != "" {
			crsByNLC[code] = strings.ToUpper(locations[code].CRS)
		}
	}
	for _, members := range clusterGroups {
		for _, member := range members {
			codes = append(codes, member.Member)
		}
	}
	stopRefs, err := stopRefsForNLC(context.Background(), codes, crsByNLC)
	if err != nil {
		return nil, nil, 0, 0, 0, err
	}

	nlcRefs := map[string]string{}
	groupStopRefs := map[string][]string{}
	count := 0
	linkedLocations, unlinkedLocations := 0, 0
	for nlc, location := range locations {
		locationRef := ctdf.FareLocationIdentifier(dataset.Identifier, ctdf.FareLocationTypeStop, "nlc-"+nlc)
		stopIDs := stopRefs[nlc]
		locationType := ctdf.FareLocationTypeStop
		if len(stopIDs) == 0 {
			locationType = ctdf.FareLocationTypeArea
			unlinkedLocations++
		} else {
			linkedLocations++
		}
		fareLocation := &ctdf.FareLocation{FareRecord: setRecordPrimary(record("location", "nlc-"+nlc, time.Time{}, time.Time{}), locationRef), Name: location.Name, Type: locationType, StopRefs: stopIDs}
		if location.FareGroup != "" && location.FareGroup != nlc {
			groupRef := ctdf.FareLocationIdentifier(dataset.Identifier, ctdf.FareLocationTypeGroup, "fare-group-"+location.FareGroup)
			fareLocation.ParentRef = groupRef
			groupStopRefs[location.FareGroup] = append(groupStopRefs[location.FareGroup], stopIDs...)
		}
		if err := upsertFare("fare_locations", locationRef, fareLocation); err != nil {
			return nil, nil, count, linkedLocations, unlinkedLocations, err
		}
		nlcRefs[nlc] = locationRef
		count++
	}
	for groupNLC, refs := range groupStopRefs {
		groupRef := ctdf.FareLocationIdentifier(dataset.Identifier, ctdf.FareLocationTypeGroup, "fare-group-"+groupNLC)
		group := &ctdf.FareLocation{FareRecord: setRecordPrimary(record("location-group", groupNLC, time.Time{}, time.Time{}), groupRef), Name: "Fare group " + groupNLC, Type: ctdf.FareLocationTypeGroup, StopRefs: uniqueStopRefs(refs)}
		if err := upsertFare("fare_locations", groupRef, group); err != nil {
			return nil, nil, count, linkedLocations, unlinkedLocations, err
		}
		nlcRefs[groupNLC] = groupRef
		count++
	}

	for groupUIC, members := range groupMembers {
		groupRef := ctdf.FareLocationIdentifier(dataset.Identifier, ctdf.FareLocationTypeGroup, "uic-"+groupUIC)
		var refs []string
		for _, memberUIC := range members {
			for _, location := range locations {
				if location.UIC == memberUIC {
					refs = append(refs, stopRefs[location.NLC]...)
				}
			}
		}
		group := &ctdf.FareLocation{FareRecord: setRecordPrimary(record("location-group", "uic-"+groupUIC, time.Time{}, time.Time{}), groupRef), Name: groupNames[groupUIC], Type: ctdf.FareLocationTypeGroup, StopRefs: uniqueStopRefs(refs)}
		if err := upsertFare("fare_locations", groupRef, group); err != nil {
			return nil, nil, count, linkedLocations, unlinkedLocations, err
		}
		count++
	}

	clusterRefs := map[string][]clusterLocation{}
	clusterCodes := make([]string, 0, len(clusterGroups))
	for cluster := range clusterGroups {
		clusterCodes = append(clusterCodes, cluster)
	}
	sort.Strings(clusterCodes)
	for _, cluster := range clusterCodes {
		for _, version := range clusterMembershipVersions(clusterGroups[cluster]) {
			sourceID := "cluster-" + cluster + "-" + dateKey(version.Start) + "-" + dateKey(version.End)
			groupRef := ctdf.FareLocationIdentifier(dataset.Identifier, ctdf.FareLocationTypeGroup, sourceID)
			var refs []string
			for _, member := range version.Members {
				refs = append(refs, stopRefs[member]...)
			}
			group := &ctdf.FareLocation{FareRecord: setRecordPrimary(record("location-cluster", sourceID, version.Start, version.End), groupRef), Name: "Fare cluster " + cluster, Type: ctdf.FareLocationTypeGroup, StopRefs: uniqueStopRefs(refs)}
			if err := upsertFare("fare_locations", groupRef, group); err != nil {
				return nil, nil, count, linkedLocations, unlinkedLocations, err
			}
			clusterRefs[cluster] = append(clusterRefs[cluster], clusterLocation{Ref: groupRef, Start: version.Start, End: version.End})
			count++
		}
	}
	return nlcRefs, clusterRefs, count, linkedLocations, unlinkedLocations, nil
}

func uniqueStopRefs(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func stopRefsForNLC(ctx context.Context, codes []string, crsByNLC map[string]string) (map[string][]string, error) {
	refs := map[string][]string{}
	identifiers := make([]string, 0, len(codes))
	seen := map[string]bool{}
	nlcByCRS := map[string]string{}
	for _, code := range codes {
		code = normalizeNLC(code)
		if code == "" {
			continue
		}
		identifier := "gb-nlc-" + code
		if !seen[identifier] {
			identifiers = append(identifiers, identifier)
			seen[identifier] = true
		}
		if crs := strings.ToUpper(strings.TrimSpace(crsByNLC[code])); crs != "" {
			crsIdentifier := "gb-crs-" + crs
			if !seen[crsIdentifier] {
				identifiers = append(identifiers, crsIdentifier)
				seen[crsIdentifier] = true
			}
			nlcByCRS[crs] = code
		}
	}
	if len(identifiers) == 0 {
		return refs, nil
	}
	cursor, err := database.GetCollection("stops").Find(ctx, bson.M{"otheridentifiers": bson.M{"$in": identifiers}}, options.Find().SetProjection(bson.M{"primaryidentifier": 1, "otheridentifiers": 1}))
	if err != nil {
		return nil, fmt.Errorf("find stops by NLC: %w", err)
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var stop ctdf.Stop
		if err := cursor.Decode(&stop); err != nil {
			return nil, fmt.Errorf("decode NLC-linked stop: %w", err)
		}
		for _, identifier := range stop.OtherIdentifiers {
			if strings.HasPrefix(identifier, "gb-nlc-") {
				refs[strings.TrimPrefix(identifier, "gb-nlc-")] = append(refs[strings.TrimPrefix(identifier, "gb-nlc-")], stop.PrimaryIdentifier)
			} else if strings.HasPrefix(identifier, "gb-crs-") {
				if nlc := nlcByCRS[strings.TrimPrefix(identifier, "gb-crs-")]; nlc != "" {
					refs[nlc] = append(refs[nlc], stop.PrimaryIdentifier)
				}
			}
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate NLC-linked stops: %w", err)
	}
	return refs, nil
}

func locationRefForNLC(code string, onDate time.Time, nlcRefs map[string]string, clusterRefs map[string][]clusterLocation) string {
	code = normalizeNLC(code)
	if clusterVersions := clusterRefs[code]; len(clusterVersions) > 0 {
		var selected clusterLocation
		found := false
		for _, cluster := range clusterVersions {
			if !fareDateMatches(cluster.Start, cluster.End, onDate) {
				continue
			}
			if !found || clusterLocationPreferred(cluster, selected) {
				selected = cluster
				found = true
			}
		}
		if found {
			return selected.Ref
		}
	}
	return nlcRefs[code]
}

func clusterLocationPreferred(candidate, selected clusterLocation) bool {
	if !candidate.Start.Equal(selected.Start) {
		return candidate.Start.After(selected.Start)
	}
	if candidate.End.IsZero() {
		return false
	}
	return selected.End.IsZero() || candidate.End.Before(selected.End)
}

func routeOnDate(routes []rspsRouteRecord, onDate time.Time) *rspsRouteRecord {
	var selected *rspsRouteRecord
	for index := range routes {
		candidate := &routes[index]
		if !fareDateMatches(candidate.Start, candidate.End, onDate) {
			continue
		}
		if selected == nil || routeRecordPreferred(*candidate, *selected) {
			selected = candidate
		}
	}
	return selected
}

func routeRecordPreferred(candidate, selected rspsRouteRecord) bool {
	if !candidate.Start.Equal(selected.Start) {
		return candidate.Start.After(selected.Start)
	}
	if !candidate.End.Equal(selected.End) {
		if candidate.End.IsZero() {
			return false
		}
		return selected.End.IsZero() || candidate.End.Before(selected.End)
	}
	if candidate.Name != selected.Name {
		return candidate.Name < selected.Name
	}
	return routeLocationsKey(candidate.Locations) < routeLocationsKey(selected.Locations)
}

func routeLocationsKey(locations []ctdf.FareRailRouteLocation) string {
	var parts []string
	for _, location := range locations {
		parts = append(parts, strings.Join([]string{location.NLC, location.CRS, location.IncludeExclude}, ":"))
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func splitRailValiditySegments(segment ticketFlowSegment, flow flowStage, nlcRefs map[string]string, clusterRefs map[string][]clusterLocation, routes map[string][]rspsRouteRecord) []ticketFlowSegment {
	if segment.Start.IsZero() || segment.End.IsZero() {
		segment.OriginRef = locationRefForNLC(flow.Origin, time.Time{}, nlcRefs, clusterRefs)
		segment.DestinationRef = locationRefForNLC(flow.Destination, time.Time{}, nlcRefs, clusterRefs)
		segment.Route = routeOnDate(routes[flow.RouteCode], time.Time{})
		return []ticketFlowSegment{segment}
	}
	start := time.Date(segment.Start.Year(), segment.Start.Month(), segment.Start.Day(), 0, 0, 0, 0, time.UTC)
	end := time.Date(segment.End.Year(), segment.End.Month(), segment.End.Day(), 0, 0, 0, 0, time.UTC)
	if end.Before(start) {
		return nil
	}
	endExclusive := end.AddDate(0, 0, 1)
	boundaries := []time.Time{start, endExclusive}
	addValidityBoundaries := func(from, to time.Time) {
		if !from.IsZero() && from.After(start) && from.Before(endExclusive) {
			boundaries = append(boundaries, from)
		}
		if !to.IsZero() {
			nextDay := to.AddDate(0, 0, 1)
			if nextDay.After(start) && nextDay.Before(endExclusive) {
				boundaries = append(boundaries, nextDay)
			}
		}
	}
	for _, route := range routes[flow.RouteCode] {
		addValidityBoundaries(route.Start, route.End)
	}
	for _, code := range []string{flow.Origin, flow.Destination} {
		for _, cluster := range clusterRefs[normalizeNLC(code)] {
			addValidityBoundaries(cluster.Start, cluster.End)
		}
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].Before(boundaries[j]) })
	uniqueBoundaries := boundaries[:0]
	for _, boundary := range boundaries {
		if len(uniqueBoundaries) == 0 || !uniqueBoundaries[len(uniqueBoundaries)-1].Equal(boundary) {
			uniqueBoundaries = append(uniqueBoundaries, boundary)
		}
	}
	segments := make([]ticketFlowSegment, 0, len(uniqueBoundaries)-1)
	for index := 0; index+1 < len(uniqueBoundaries); index++ {
		segmentStart := uniqueBoundaries[index]
		segmentEnd := uniqueBoundaries[index+1].AddDate(0, 0, -1)
		subsegment := segment
		subsegment.Start = segmentStart
		subsegment.End = segmentEnd
		subsegment.OriginRef = locationRefForNLC(flow.Origin, segmentStart, nlcRefs, clusterRefs)
		subsegment.DestinationRef = locationRefForNLC(flow.Destination, segmentStart, nlcRefs, clusterRefs)
		subsegment.Route = routeOnDate(routes[flow.RouteCode], segmentStart)
		segments = append(segments, subsegment)
	}
	return segments
}

func normalizeNLC(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code != "" && len(code) < 4 {
		code = strings.Repeat("0", 4-len(code)) + code
	}
	return code
}

func (format *Fares) stageFlowsAndFares(ctx context.Context, file *zip.File, datasource *ctdf.DataSourceReference) error {
	runID := datasource.FareVersionID
	flowCollection := database.GetCollection(flowStageCollection)
	fareCollection := database.GetCollection(fareStageCollection)
	if _, err := flowCollection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "runid", Value: 1}, {Key: "flowid", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		return fmt.Errorf("index RSPS5045 flow staging: %w", err)
	}
	if _, err := fareCollection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "runid", Value: 1}, {Key: "flowid", Value: 1}, {Key: "ticketcode", Value: 1}, {Key: "restrictioncode", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		return fmt.Errorf("index RSPS5045 fare staging: %w", err)
	}
	if err := ensureFareImportLease(ctx, datasource); err != nil {
		return err
	}
	if _, err := flowCollection.DeleteMany(ctx, bson.M{"runid": runID}); err != nil {
		return fmt.Errorf("clear RSPS5045 flow staging: %w", err)
	}
	if err := ensureFareImportLease(ctx, datasource); err != nil {
		return err
	}
	if _, err := fareCollection.DeleteMany(ctx, bson.M{"runid": runID}); err != nil {
		return fmt.Errorf("clear RSPS5045 fare staging: %w", err)
	}
	var flowWrites, fareWrites []mongo.WriteModel
	flush := func(collection *mongo.Collection, writes *[]mongo.WriteModel) error {
		if len(*writes) == 0 {
			return nil
		}
		if err := ensureFareImportLease(ctx, datasource); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := collection.BulkWrite(ctx, *writes, options.BulkWrite().SetOrdered(false))
		*writes = nil
		return err
	}
	_, _, err := scanFile(file, file.Name, "FFL", map[byte]int{'F': 49, 'T': 22}, func(line []byte, _ int64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if line[0] != 'R' {
			return fmt.Errorf("FFL update marker %q is not valid in a full refresh", line[0])
		}
		switch line[1] {
		case 'F':
			flowID := strings.TrimSpace(string(line[42:49]))
			if flowID == "" {
				return errors.New("flow record has no FLOW_ID")
			}
			from, err := dateField(line[28:36])
			if err != nil {
				return err
			}
			to, err := dateField(line[20:28])
			if err != nil {
				return err
			}
			if !from.IsZero() && !to.IsZero() && to.Before(from) {
				return fmt.Errorf("flow %s ends before it starts", flowID)
			}
			flow := flowStage{RunID: runID, FlowID: flowID, Origin: normalizeNLC(string(line[2:6])), Destination: normalizeNLC(string(line[6:10])), RouteCode: strings.TrimSpace(string(line[10:15])), StatusCode: strings.TrimSpace(string(line[15:18])), UsageCode: strings.TrimSpace(string(line[18:19])), Direction: strings.TrimSpace(string(line[19:20])), StartDate: from, EndDate: to, FareTOC: strings.TrimSpace(string(line[36:39])), ViaLondon: strings.TrimSpace(string(line[39:40])), DiscountFlag: strings.TrimSpace(string(line[40:41]))}
			model := mongo.NewReplaceOneModel().SetFilter(bson.M{"runid": runID, "flowid": flowID}).SetReplacement(flow).SetUpsert(true)
			flowWrites = append(flowWrites, model)
			if len(flowWrites) >= writeBatchSize {
				if err := flush(flowCollection, &flowWrites); err != nil {
					return fmt.Errorf("stage FFL flow records: %w", err)
				}
			}
		case 'T':
			flowID := strings.TrimSpace(string(line[2:9]))
			ticketCode := strings.TrimSpace(string(line[9:12]))
			priceText := strings.TrimSpace(string(line[12:20]))
			if flowID == "" || ticketCode == "" || priceText == "" {
				return errors.New("fare record is missing FLOW_ID, TICKET_CODE or FARE")
			}
			amount, err := strconv.ParseInt(priceText, 10, 64)
			if err != nil || amount < 0 {
				return fmt.Errorf("fare %s/%s has invalid pence amount %q", flowID, ticketCode, priceText)
			}
			fare := fareStage{RunID: runID, FlowID: flowID, TicketCode: ticketCode, AmountPence: amount, RestrictionCode: strings.TrimSpace(string(line[20:22]))}
			key := bson.M{"runid": runID, "flowid": flowID, "ticketcode": ticketCode, "restrictioncode": fare.RestrictionCode}
			model := mongo.NewReplaceOneModel().SetFilter(key).SetReplacement(fare).SetUpsert(true)
			fareWrites = append(fareWrites, model)
			if len(fareWrites) >= writeBatchSize {
				if err := flush(fareCollection, &fareWrites); err != nil {
					return fmt.Errorf("stage FFL fare records: %w", err)
				}
			}
		default:
			return fmt.Errorf("unsupported FFL record type %q", line[1])
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := flush(flowCollection, &flowWrites); err != nil {
		return fmt.Errorf("flush FFL flow records: %w", err)
	}
	if err := flush(fareCollection, &fareWrites); err != nil {
		return fmt.Errorf("flush FFL fare records: %w", err)
	}
	return nil
}

type fareBulkWriter struct {
	collection *mongo.Collection
	datasetID  string
	versionID  string
	ctx        context.Context
	datasource *ctdf.DataSourceReference
	models     []mongo.WriteModel
	count      int
}

func newFareBulkWriter(ctx context.Context, datasource *ctdf.DataSourceReference, collection string) *fareBulkWriter {
	return &fareBulkWriter{collection: database.GetCollection(collection), datasetID: datasource.DatasetID, versionID: datasource.FareVersionID, ctx: ctx, datasource: datasource, models: make([]mongo.WriteModel, 0, writeBatchSize)}
}

func (writer *fareBulkWriter) add(identifier string, value any) error {
	model := mongo.NewReplaceOneModel().SetFilter(bson.M{"primaryidentifier": identifier, "datasetid": writer.datasetID, "fareversionid": writer.versionID}).SetReplacement(value).SetUpsert(true)
	writer.models = append(writer.models, model)
	writer.count++
	if len(writer.models) >= writeBatchSize {
		return writer.flush()
	}
	return nil
}

func (writer *fareBulkWriter) flush() error {
	if len(writer.models) == 0 {
		return nil
	}
	if err := writer.ctx.Err(); err != nil {
		return err
	}
	if err := ensureFareImportLease(writer.ctx, writer.datasource); err != nil {
		return err
	}
	_, err := writer.collection.BulkWrite(writer.ctx, writer.models, options.BulkWrite().SetOrdered(false))
	writer.models = writer.models[:0]
	return err
}

func (format *Fares) writeFlowFares(dataset datasets.DataSet, datasource *ctdf.DataSourceReference, record fareRecordFactory, tickets map[string][]ticketType, adultRef string, nlcRefs map[string]string, clusterRefs map[string][]clusterLocation, routes map[string][]rspsRouteRecord, fareTOCRefs map[string]string, railRestrictions map[string][]ctdf.FareRailRestriction, advancePurchase map[string][]advancePurchaseRecord, tocSpecificTickets map[string][]tocSpecificTicketRecord, report *datasets.DataImportReport) error {
	ctx := datasource.ImportContext
	if ctx == nil {
		ctx = context.Background()
	}
	runID := datasource.FareVersionID
	fareCollection := database.GetCollection(fareStageCollection)
	expectedFares, err := fareCollection.CountDocuments(ctx, bson.M{"runid": runID})
	if err != nil {
		return fmt.Errorf("count staged RSPS5045 fares: %w", err)
	}
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.M{"runid": runID}}},
		bson.D{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: flowStageCollection},
			{Key: "let", Value: bson.D{{Key: "flowid", Value: "$flowid"}, {Key: "runid", Value: "$runid"}}},
			{Key: "pipeline", Value: mongo.Pipeline{bson.D{{Key: "$match", Value: bson.M{"$expr": bson.M{"$and": bson.A{bson.M{"$eq": bson.A{"$flowid", "$$flowid"}}, bson.M{"$eq": bson.A{"$runid", "$$runid"}}}}}}}}},
			{Key: "as", Value: "flow"},
		}}},
		bson.D{{Key: "$unwind", Value: "$flow"}},
		bson.D{{Key: "$project", Value: bson.M{"_id": 0, "flow": "$flow", "fare": "$$ROOT"}}},
	}
	cursor, err := fareCollection.Aggregate(ctx, pipeline, options.Aggregate().SetAllowDiskUse(true).SetBatchSize(writeBatchSize))
	if err != nil {
		return fmt.Errorf("join staged RSPS5045 flows and fares: %w", err)
	}
	defer cursor.Close(ctx)
	offerWriter := newFareBulkWriter(ctx, datasource, "fare_offers")
	ruleWriter := newFareBulkWriter(ctx, datasource, "fare_leg_rules")
	var joined int64
	for cursor.Next(ctx) {
		if err := ctx.Err(); err != nil {
			return err
		}
		var row flowFare
		if err := cursor.Decode(&row); err != nil {
			return fmt.Errorf("decode joined RSPS5045 fare: %w", err)
		}
		joined++
		flow, fare := row.Flow, row.Fare
		if flow.StatusCode != "000" {
			continue
		}
		segments, gaps, err := ticketSegmentsForFlow(tickets, fare.TicketCode, flow.StartDate, flow.EndDate)
		if err != nil {
			return fmt.Errorf("FFL fare %s/%s: %w", flow.FlowID, fare.TicketCode, err)
		}
		for _, gap := range gaps {
			gapDetail := fmt.Sprintf("FFL flow %s ticket %s: %s to %s has no valid TTY definition", flow.FlowID, fare.TicketCode, fareDateLabel(gap.Start), fareDateLabel(gap.End))
			report.AddFareValidityGap(gapDetail)
		}
		if len(segments) == 0 {
			continue
		}
		var railSegments []ticketFlowSegment
		for _, segment := range segments {
			railSegments = append(railSegments, splitRailValiditySegments(segment, flow, nlcRefs, clusterRefs, routes)...)
		}
		price := ctdf.FareMoney{AmountMinor: fare.AmountPence, Currency: "GBP", Exponent: 2}
		if err := price.Validate(); err != nil {
			return fmt.Errorf("FFL fare %s/%s: %w", flow.FlowID, fare.TicketCode, err)
		}
		for _, segment := range railSegments {
			originRef := segment.OriginRef
			if originRef == "" {
				originRef, err = ensureAreaLocation(dataset, datasource, record, flow.Origin)
				if err != nil {
					return err
				}
				report.ImportedFareLocations++
			}
			destinationRef := segment.DestinationRef
			if destinationRef == "" {
				destinationRef, err = ensureAreaLocation(dataset, datasource, record, flow.Destination)
				if err != nil {
					return err
				}
				report.ImportedFareLocations++
			}
			product := segment.Product
			segmentSource := dateKey(segment.Start) + "-" + dateKey(segment.End)
			offerSource := strings.Join([]string{flow.FlowID, fare.TicketCode, fare.RestrictionCode, segmentSource}, "-")
			offerID := fmt.Sprintf("%s-rsps5045-offer-%s", dataset.Identifier, safeID(offerSource))
			conditions := []string{}
			constraints := []string{}
			var railRoute *ctdf.FareRailRoute
			if flow.RouteCode != "" && flow.RouteCode != "00000" {
				name := flow.RouteCode
				if segment.Route != nil {
					if segment.Route.Name != "" {
						name = segment.Route.Name
					}
					railRoute = &ctdf.FareRailRoute{Description: segment.Route.Name, Locations: append([]ctdf.FareRailRouteLocation(nil), segment.Route.Locations...)}
				}
				conditions = append(conditions, "Route restriction: "+name)
				if railRoute == nil {
					constraints = append(constraints, "route "+flow.RouteCode)
				}
			}
			if fare.RestrictionCode != "" && len(railRestrictions[fare.RestrictionCode]) == 0 {
				conditions = append(conditions, "Restriction code: "+fare.RestrictionCode)
				constraints = append(constraints, "restriction "+fare.RestrictionCode)
			}
			if flow.ViaLondon != "" && flow.ViaLondon != "0" {
				conditions = append(conditions, "London routing condition: "+flow.ViaLondon)
				constraints = append(constraints, "London routing condition "+flow.ViaLondon)
			}
			if flow.DiscountFlag != "" && flow.DiscountFlag != "0" {
				conditions = append(conditions, "Non-standard discount conditions apply")
			}
			if flow.UsageCode == "G" {
				constraints = append(constraints, "constructed flow")
			}
			fareTOCName := fareTOCRefs[flow.FareTOC]
			if fareTOCName != "" {
				conditions = append(conditions, "Fare setting TOC: "+fareTOCName)
			}
			offer := &ctdf.FareOffer{FareRecord: setRecordPrimary(record("offer", offerSource, segment.Start, segment.End), offerID), ProductRef: product.ProductRef, Price: price, Availability: ctdf.FareAvailabilityUnknown, SalesConditions: conditions, SalesValidFrom: segment.Start, SalesValidTo: inclusiveEndOfDay(segment.End)}
			if err := offerWriter.add(offerID, offer); err != nil {
				return fmt.Errorf("write RSPS5045 offers: %w", err)
			}
			ruleID := fmt.Sprintf("%s-rsps5045-rule-%s", dataset.Identifier, safeID(offerSource))
			railConditions := &ctdf.FareRailConditions{
				TicketCode:           fare.TicketCode,
				RouteCode:            flow.RouteCode,
				Route:                railRoute,
				RestrictionCode:      fare.RestrictionCode,
				FareSettingTOC:       flow.FareTOC,
				CrossLondonIndicator: flow.ViaLondon,
				UsageCode:            flow.UsageCode,
				NonStandardDiscount:  flow.DiscountFlag != "" && flow.DiscountFlag != "0",
				Restrictions:         railRestrictions[fare.RestrictionCode],
				TicketCalendars:      product.RailTicket.Calendars,
				AdvancePurchase:      matchingAdvancePurchase(advancePurchase[fare.TicketCode], fare.RestrictionCode),
				TOCSpecificTickets:   matchingTOCSpecificTickets(tocSpecificTickets[fare.TicketCode], fare.RestrictionCode),
			}
			rule := &ctdf.FareLegRule{FareRecord: setRecordPrimary(record("rule", offerSource, segment.Start, segment.End), ruleID), ProductRef: product.ProductRef, OfferRef: offerID, OriginRef: originRef, DestinationRef: destinationRef, RiderRef: adultRef, UnresolvedConstraints: uniqueStopRefs(constraints), RailConditions: railConditions}
			if err := ruleWriter.add(rule.PrimaryIdentifier, rule); err != nil {
				return fmt.Errorf("write RSPS5045 leg rules: %w", err)
			}
			report.ImportedFareOffers++
			report.ImportedFareRules++
			if flow.Direction == "R" {
				reverseSource := offerSource + "-reverse"
				reverseID := fmt.Sprintf("%s-rsps5045-rule-%s", dataset.Identifier, safeID(reverseSource))
				reverseRule := &ctdf.FareLegRule{FareRecord: setRecordPrimary(record("rule", reverseSource, segment.Start, segment.End), reverseID), ProductRef: product.ProductRef, OfferRef: offerID, OriginRef: destinationRef, DestinationRef: originRef, RiderRef: adultRef, UnresolvedConstraints: uniqueStopRefs(constraints), RailConditions: railConditions}
				if err := ruleWriter.add(reverseRule.PrimaryIdentifier, reverseRule); err != nil {
					return fmt.Errorf("write reverse RSPS5045 leg rules: %w", err)
				}
				report.ImportedFareRules++
			}
		}
	}
	if err := cursor.Err(); err != nil {
		return fmt.Errorf("iterate joined RSPS5045 fares: %w", err)
	}
	if joined != expectedFares {
		return fmt.Errorf("RSPS5045 FFL contains %d fare rows but only %d join to flow records", expectedFares, joined)
	}
	if err := offerWriter.flush(); err != nil {
		return fmt.Errorf("flush RSPS5045 offers: %w", err)
	}
	if err := ruleWriter.flush(); err != nil {
		return fmt.Errorf("flush RSPS5045 leg rules: %w", err)
	}
	if report.ImportedFareOffers == 0 {
		if report.UncoveredFareValidityIntervals > 0 {
			examples := strings.Join(report.FareValidityGapDetails, "; ")
			if examples == "" {
				examples = "no examples retained"
			}
			return fmt.Errorf("RSPS5045 contains no usable adult fares; %d ticket-validity intervals are uncovered: %s", report.UncoveredFareValidityIntervals, examples)
		}
		return errors.New("RSPS5045 full feed contains no adult fares to import")
	}
	return nil
}

func ensureAreaLocation(dataset datasets.DataSet, datasource *ctdf.DataSourceReference, record fareRecordFactory, nlc string) (string, error) {
	if nlc == "" {
		return "", errors.New("flow endpoint is missing an NLC location")
	}
	ref := ctdf.FareLocationIdentifier(dataset.Identifier, ctdf.FareLocationTypeArea, "nlc-"+nlc)
	location := &ctdf.FareLocation{FareRecord: setRecordPrimary(record("location", "nlc-"+nlc, time.Time{}, time.Time{}), ref), Name: nlc, Type: ctdf.FareLocationTypeArea}
	if err := upsertFare("fare_locations", ref, location); err != nil {
		return "", err
	}
	return ref, nil
}

func inclusiveEndOfDay(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return time.Date(value.Year(), value.Month(), value.Day(), 23, 59, 59, 0, value.Location())
}

func setRecordPrimary(record ctdf.FareRecord, identifier string) ctdf.FareRecord {
	record.PrimaryIdentifier = identifier
	return record
}

func safeID(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "/", "_")
	return value
}

func upsertFare(collectionName, identifier string, value any) error {
	versioned, ok := value.(interface {
		GetFareDatasetID() string
		GetFareVersionID() string
	})
	if !ok || versioned.GetFareDatasetID() == "" || versioned.GetFareVersionID() == "" {
		return fmt.Errorf("fare record %s has no dataset or version identity", identifier)
	}
	ctx := context.Background()
	if withRecord, ok := value.(interface{ GetFareRecord() ctdf.FareRecord }); ok {
		record := withRecord.GetFareRecord()
		if record.DataSource != nil {
			ctx = record.DataSource.GetImportContext()
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if withRecord, ok := value.(interface{ GetFareRecord() ctdf.FareRecord }); ok {
		if err := ensureFareImportLease(ctx, withRecord.GetFareRecord().DataSource); err != nil {
			return err
		}
	}
	_, err := database.GetCollection(collectionName).ReplaceOne(ctx, bson.M{
		"primaryidentifier": identifier,
		"datasetid":         versioned.GetFareDatasetID(),
		"fareversionid":     versioned.GetFareVersionID(),
	}, value, options.Replace().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("store RSPS5045 fare record %s: %w", identifier, err)
	}
	return nil
}

func ensureFareImportLease(ctx context.Context, datasource *ctdf.DataSourceReference) error {
	return fareversionlease.EnsureImportLease(ctx, datasource)
}
