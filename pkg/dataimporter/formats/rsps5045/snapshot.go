package rsps5045

import (
	"archive/zip"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const sourceRecordCollection = "rsps5045_source_records"

// sourceRecord is the durable raw snapshot from which each immutable fare
// version is rebuilt. Keeping only the source rows needed by the current
// RSPS5045 converter makes daily deltas replayable without retaining a whole
// ZIP in application memory.
type sourceRecord struct {
	DatasetID     string `bson:"datasetid"`
	FareVersionID string `bson:"fareversionid"`
	FileType      string `bson:"filetype"`
	SourceKey     string `bson:"sourcekey"`
	RecordType    string `bson:"recordtype,omitempty"`
	FlowID        string `bson:"flowid,omitempty"`
	Record        string `bson:"record"`
}

var snapshotFileTypes = []string{"DIS", "FFL", "FNS", "FSC", "LOC", "RCM", "RLC", "RST", "RTE", "TAP", "TOC", "TSP", "TTY", "TVL"}

var fullOnlySnapshotTypes = map[string]bool{"DIS": true, "RCM": true, "RLC": true, "TAP": true, "TOC": true, "TSP": true, "TVL": true}

var snapshotMarkerTypes = map[string]bool{"FFL": true, "FNS": true, "FSC": true, "LOC": true, "RST": true, "RTE": true, "TTY": true}

// prepareSourceArchive seeds or merges the raw source snapshot, then exposes
// the merged rows as a synthetic full-refresh ZIP to the existing importer.
// This keeps all conversion paths identical for full and incremental feeds.
func (format *Fares) prepareSourceArchive(ctx context.Context, datasetID, fareVersionID string, datasource *ctdf.DataSourceReference) (func(), error) {
	started := time.Now()
	collection := database.GetCollection(sourceRecordCollection)
	validateRequiredRows := func() error {
		for _, fileType := range []string{"FFL", "FSC", "LOC", "RTE", "TTY"} {
			count, err := collection.CountDocuments(ctx, bson.M{"datasetid": datasetID, "fareversionid": fareVersionID, "filetype": fileType})
			if err != nil {
				return fmt.Errorf("count RSPS5045 %s source rows: %w", fileType, err)
			}
			if count == 0 {
				return fmt.Errorf("RSPS5045 source snapshot is missing required %s rows; import a full refresh", fileType)
			}
		}
		return nil
	}
	if format.mode == 'F' {
		log.Info().Str("dataset", datasetID).Str("sequence", format.seq).Msg("Storing full RSPS5045 source snapshot")
		if err := ensureFareImportLease(ctx, datasource); err != nil {
			return nil, err
		}
		if _, err := collection.DeleteMany(ctx, bson.M{"datasetid": datasetID, "fareversionid": fareVersionID}); err != nil {
			return nil, fmt.Errorf("clear RSPS5045 source snapshot: %w", err)
		}
		for _, fileType := range snapshotFileTypes {
			files := format.filesByExtension("." + fileType)
			if len(files) > 1 {
				return nil, fmt.Errorf("RSPS5045 full feed has multiple %s files", fileType)
			}
			if len(files) == 1 {
				if err := storeFullSnapshotFile(ctx, files[0], datasetID, fareVersionID, fileType, datasource); err != nil {
					return nil, err
				}
			}
		}
		// The downloaded full refresh is already the archive we need to import.
		// Rebuilding it from Mongo would scan and rewrite the entire FFL file
		// before the importer scans that same file again.
		if err := validateRequiredRows(); err != nil {
			return nil, err
		}
		log.Info().Str("dataset", datasetID).Str("sequence", format.seq).Dur("elapsed", time.Since(started)).Msg("Stored full RSPS5045 source snapshot")
		return func() {}, nil
	} else {
		log.Info().Str("dataset", datasetID).Str("sequence", format.seq).Msg("Merging RSPS5045 change feed into source snapshot")
		var active ctdf.FareVersionHead
		if err := database.GetCollection("fare_versions").FindOne(ctx, bson.M{"datasetid": datasetID}).Decode(&active); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, errors.New("RSPS5045 change feed has no active full-refresh baseline; import a full feed first")
			}
			return nil, fmt.Errorf("read active RSPS5045 feed sequence: %w", err)
		}
		if active.ActiveVersionID == "" || active.SourceSequence == "" {
			return nil, errors.New("active RSPS5045 fare version has no source snapshot or feed sequence; import a full feed first")
		}
		if !isNextSequence(active.SourceSequence, format.seq) {
			return nil, fmt.Errorf("RSPS5045 change feed sequence %s does not follow active sequence %s; request a full refresh", format.seq, active.SourceSequence)
		}
		if err := cloneSourceSnapshot(ctx, active.ActiveVersionID, datasetID, fareVersionID, datasource); err != nil {
			return nil, err
		}
		for _, fileType := range snapshotFileTypes {
			files := format.filesByExtension("." + fileType)
			if len(files) > 1 {
				return nil, fmt.Errorf("RSPS5045 change feed has multiple %s files", fileType)
			}
			if len(files) == 1 {
				if err := mergeSnapshotFile(ctx, files[0], datasetID, fareVersionID, fileType, datasource); err != nil {
					return nil, err
				}
			}
		}
	}

	if err := validateRequiredRows(); err != nil {
		return nil, err
	}

	buildStarted := time.Now()
	file, err := buildSnapshotArchive(ctx, datasetID, fareVersionID, format.seq)
	if err != nil {
		return nil, err
	}
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		os.Remove(file.Name())
		return nil, fmt.Errorf("stat merged RSPS5045 archive: %w", err)
	}
	archive, err := zip.NewReader(file, stat.Size())
	if err != nil {
		file.Close()
		os.Remove(file.Name())
		return nil, fmt.Errorf("open merged RSPS5045 archive: %w", err)
	}
	files := make(map[string]*zip.File, len(archive.File))
	for _, member := range archive.File {
		files[strings.ToUpper(filepath.Base(member.Name))] = member
	}
	format.archive = archive
	format.files = files
	format.mode = 'F'
	log.Info().Str("dataset", datasetID).Str("sequence", format.seq).Dur("elapsed", time.Since(buildStarted)).Dur("total_elapsed", time.Since(started)).Msg("Built merged RSPS5045 import archive")
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}
	return cleanup, nil
}

func isNextSequence(previous, current string) bool {
	oldValue, oldErr := strconv.Atoi(previous)
	newValue, newErr := strconv.Atoi(current)
	if oldErr != nil || newErr != nil || oldValue < 0 || oldValue > 999 || newValue < 0 || newValue > 999 {
		return false
	}
	return newValue == (oldValue+1)%1000
}

func cloneSourceSnapshot(ctx context.Context, fromVersion, datasetID, toVersion string, datasource *ctdf.DataSourceReference) error {
	collection := database.GetCollection(sourceRecordCollection)
	if err := ensureFareImportLease(ctx, datasource); err != nil {
		return err
	}
	if _, err := collection.DeleteMany(ctx, bson.M{"datasetid": datasetID, "fareversionid": toVersion}); err != nil {
		return fmt.Errorf("clear candidate RSPS5045 source snapshot: %w", err)
	}
	cursor, err := collection.Find(ctx, bson.M{"datasetid": datasetID, "fareversionid": fromVersion}, options.Find().SetSort(bson.D{{Key: "sourcekey", Value: 1}}).SetBatchSize(writeBatchSize))
	if err != nil {
		return fmt.Errorf("read active RSPS5045 source snapshot: %w", err)
	}
	defer cursor.Close(ctx)
	var writes []mongo.WriteModel
	for cursor.Next(ctx) {
		var row sourceRecord
		if err := cursor.Decode(&row); err != nil {
			return fmt.Errorf("decode active RSPS5045 source row: %w", err)
		}
		row.FareVersionID = toVersion
		if (row.FileType == "RTE" && row.RecordType == "L") || row.FileType == "FSC" {
			canonicalKey, recordType, _, keyErr := snapshotRecordKey(row.FileType, []byte(row.Record))
			if keyErr != nil {
				return fmt.Errorf("rekey active %s source row: %w", row.FileType, keyErr)
			}
			if row.FileType == "RTE" && recordType != "L" {
				return fmt.Errorf("RTE snapshot row %q is not a location record", row.SourceKey)
			}
			row.SourceKey = canonicalKey
		}
		writes = append(writes, mongo.NewReplaceOneModel().SetFilter(bson.M{
			"datasetid": row.DatasetID, "fareversionid": toVersion, "filetype": row.FileType, "sourcekey": row.SourceKey,
		}).SetReplacement(row).SetUpsert(true))
		if len(writes) >= writeBatchSize {
			if err := writeSourceRows(ctx, collection, writes, datasource); err != nil {
				return err
			}
			writes = nil
		}
	}
	if err := cursor.Err(); err != nil {
		return fmt.Errorf("iterate active RSPS5045 source snapshot: %w", err)
	}
	if err := writeSourceRows(ctx, collection, writes, datasource); err != nil {
		return err
	}
	count, err := collection.CountDocuments(ctx, bson.M{"datasetid": datasetID, "fareversionid": toVersion})
	if err != nil {
		return fmt.Errorf("verify candidate RSPS5045 source snapshot: %w", err)
	}
	if count == 0 {
		return errors.New("active RSPS5045 fare version has no saved source snapshot; import a full feed first")
	}
	return nil
}

func storeFullSnapshotFile(ctx context.Context, file *zip.File, datasetID, fareVersionID, fileType string, datasource *ctdf.DataSourceReference) error {
	collection := database.GetCollection(sourceRecordCollection)
	if err := ensureFareImportLease(ctx, datasource); err != nil {
		return err
	}
	if _, err := collection.DeleteMany(ctx, bson.M{"datasetid": datasetID, "fareversionid": fareVersionID, "filetype": fileType}); err != nil {
		return fmt.Errorf("clear RSPS5045 %s source rows: %w", fileType, err)
	}
	var writes []mongo.WriteModel
	_, _, err := scanFile(file, file.Name, fileType, nil, func(line []byte, _ int64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, recordType, flowID, keyErr := snapshotRecordKey(fileType, line)
		if keyErr != nil {
			return keyErr
		}
		row := sourceRecord{DatasetID: datasetID, FareVersionID: fareVersionID, FileType: fileType, SourceKey: key, RecordType: recordType, FlowID: flowID, Record: string(line)}
		writes = append(writes, mongo.NewReplaceOneModel().SetFilter(bson.M{
			"datasetid": datasetID, "fareversionid": fareVersionID, "filetype": fileType, "sourcekey": key,
		}).SetReplacement(row).SetUpsert(true))
		if len(writes) >= writeBatchSize {
			if err := writeSourceRows(ctx, collection, writes, datasource); err != nil {
				return err
			}
			writes = nil
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("store RSPS5045 %s full-refresh rows: %w", fileType, err)
	}
	if err := writeSourceRows(ctx, collection, writes, datasource); err != nil {
		return err
	}
	return nil
}

func mergeSnapshotFile(ctx context.Context, file *zip.File, datasetID, fareVersionID, fileType string, datasource *ctdf.DataSourceReference) error {
	collection := database.GetCollection(sourceRecordCollection)
	allRefresh := true
	seenUpdate := false
	_, _, err := scanFile(file, file.Name, fileType, nil, func(line []byte, _ int64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if fileType == "TOC" {
			return nil
		}
		if len(line) == 0 {
			return errors.New("empty source record")
		}
		if line[0] != 'R' {
			allRefresh = false
		}
		seenUpdate = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("inspect RSPS5045 %s change file: %w", fileType, err)
	}
	if fullOnlySnapshotTypes[fileType] || (seenUpdate && allRefresh) {
		return storeFullSnapshotFile(ctx, file, datasetID, fareVersionID, fileType, datasource)
	}
	if allRefresh && !seenUpdate {
		// Empty change files carry no changes and leave the active table intact.
		return nil
	}

	var writes []mongo.WriteModel
	flush := func() error {
		if err := writeSourceRows(ctx, collection, writes, datasource); err != nil {
			return err
		}
		writes = nil
		return nil
	}
	_, _, err = scanFile(file, file.Name, fileType, nil, func(line []byte, _ int64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(line) < 2 {
			return errors.New("source record is shorter than its marker and type")
		}
		marker := line[0]
		if marker != 'I' && marker != 'A' && marker != 'D' {
			return fmt.Errorf("mixed refresh and change markers in %s file", fileType)
		}
		key, recordType, flowID, keyErr := snapshotRecordKey(fileType, line)
		if keyErr != nil {
			return keyErr
		}
		filter := bson.M{"datasetid": datasetID, "fareversionid": fareVersionID, "filetype": fileType, "sourcekey": key}
		if fileType == "FFL" && recordType == "T" {
			// Older snapshots keyed T rows by restriction code as well. FFL
			// identifies a fare by FLOW_ID and TICKET_CODE, so remove any legacy
			// variants whenever this ticket row is changed or deleted.
			flowID := strings.TrimSpace(string(line[2:9]))
			ticketCode := strings.TrimSpace(string(line[9:12]))
			legacyKey := "^T:" + regexp.QuoteMeta(flowID) + ":" + regexp.QuoteMeta(ticketCode) + ":"
			writes = append(writes, mongo.NewDeleteManyModel().SetFilter(bson.M{
				"datasetid": datasetID, "fareversionid": fareVersionID, "filetype": fileType,
				"sourcekey": bson.M{"$regex": legacyKey},
			}))
		}
		if fileType == "RST" && (recordType == "HD" || recordType == "TD" || recordType == "SD") {
			// Legacy keys included DAYS. These date-band records are keyed by
			// their date range, while weekday flags are replaceable payload.
			legacyKey := "^" + regexp.QuoteMeta(key) + ".{1,7}$"
			writes = append(writes, mongo.NewDeleteManyModel().SetFilter(bson.M{
				"datasetid": datasetID, "fareversionid": fareVersionID, "filetype": fileType,
				"sourcekey": bson.M{"$regex": legacyKey},
			}))
		}
		if marker == 'D' {
			writes = append(writes, mongo.NewDeleteOneModel().SetFilter(filter))
			if fileType == "FFL" && recordType == "F" {
				writes = append(writes, mongo.NewDeleteManyModel().SetFilter(bson.M{"datasetid": datasetID, "fareversionid": fareVersionID, "filetype": fileType, "flowid": flowID, "recordtype": "T"}))
			}
		} else {
			refresh := append([]byte(nil), line...)
			refresh[0] = 'R'
			row := sourceRecord{DatasetID: datasetID, FareVersionID: fareVersionID, FileType: fileType, SourceKey: key, RecordType: recordType, FlowID: flowID, Record: string(refresh)}
			writes = append(writes, mongo.NewReplaceOneModel().SetFilter(filter).SetReplacement(row).SetUpsert(true))
		}
		if len(writes) >= writeBatchSize {
			return flush()
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("merge RSPS5045 %s changes: %w", fileType, err)
	}
	if err := flush(); err != nil {
		return fmt.Errorf("flush RSPS5045 %s changes: %w", fileType, err)
	}
	return nil
}

func snapshotRecordKey(fileType string, line []byte) (key, recordType, flowID string, err error) {
	field := func(start, end int) string {
		if len(line) < end {
			return ""
		}
		return strings.TrimSpace(string(line[start:end]))
	}
	if len(line) < 2 {
		return "", "", "", errors.New("record is too short to identify")
	}
	switch fileType {
	case "FFL":
		recordType = string(line[1])
		if recordType == "F" {
			if len(line) < 49 {
				return "", "", "", fmt.Errorf("FFL flow record has length %d; expected at least 49", len(line))
			}
			flowID = field(42, 49)
			if flowID == "" {
				return "", "", "", errors.New("FFL flow row has no FLOW_ID")
			}
			return "F:" + flowID, recordType, flowID, nil
		}
		if recordType == "T" {
			if len(line) < 22 {
				return "", "", "", fmt.Errorf("FFL fare record has length %d; expected at least 22", len(line))
			}
			flowID = field(2, 9)
			ticket := field(9, 12)
			if flowID == "" || ticket == "" {
				return "", "", "", errors.New("FFL fare row has no FLOW_ID or TICKET_CODE")
			}
			return strings.Join([]string{"T", flowID, ticket}, ":"), recordType, flowID, nil
		}
		return "", "", "", fmt.Errorf("unsupported FFL record type %q", recordType)
	case "FNS":
		if len(line) < 68 {
			return "", "", "", fmt.Errorf("FNS record has length %d; expected at least 68", len(line))
		}
		return "FNS:" + field(1, 28), "FNS", "", nil
	case "TTY":
		if len(line) < 12 {
			return "", "", "", fmt.Errorf("TTY record has length %d; expected at least 12", len(line))
		}
		code, endDate := field(1, 4), field(4, 12)
		if code == "" || endDate == "" {
			return "", "", "", errors.New("TTY row has no ticket code or end date")
		}
		return "TTY:" + code + ":" + endDate, "TTY", "", nil
	case "FSC":
		if len(line) < 25 {
			return "", "", "", fmt.Errorf("FSC record has length %d; expected at least 25", len(line))
		}
		return "FSC:" + field(1, 17), "FSC", "", nil
	case "LOC":
		if len(line) < 2 {
			return "", "", "", errors.New("LOC row has no record type")
		}
		recordType = string(line[1])
		switch recordType {
		case "L":
			if len(line) < 25 {
				return "", "", "", fmt.Errorf("LOC L record has length %d; expected at least 25", len(line))
			}
			return "L:" + field(2, 9) + ":" + field(9, 17), recordType, "", nil
		case "G":
			if len(line) < 33 {
				return "", "", "", fmt.Errorf("LOC G record has length %d; expected at least 33", len(line))
			}
			return "G:" + field(2, 9) + ":" + field(9, 17), recordType, "", nil
		case "M":
			if len(line) < 27 {
				return "", "", "", fmt.Errorf("LOC M record has length %d; expected at least 27", len(line))
			}
			return "M:" + field(2, 9) + ":" + field(9, 17) + ":" + field(17, 24), recordType, "", nil
		case "R":
			if len(line) < 20 {
				return "", "", "", fmt.Errorf("LOC R record has length %d; expected at least 20", len(line))
			}
			return "R:" + field(2, 9) + ":" + field(9, 12) + ":" + field(12, 20), recordType, "", nil
		case "S":
			if len(line) < 41 {
				return "", "", "", fmt.Errorf("LOC S record has length %d; expected at least 41", len(line))
			}
			return "S:" + field(2, 9) + ":" + field(9, 17) + ":" + field(17, 25), recordType, "", nil
		default:
			return "", "", "", fmt.Errorf("LOC record type %q does not have a supported change key", recordType)
		}
	case "RTE":
		recordType = string(line[1])
		if len(line) < 25 {
			return "", "", "", fmt.Errorf("RTE record has length %d; expected at least 25", len(line))
		}
		if recordType != "R" && recordType != "L" {
			return "", "", "", fmt.Errorf("RTE record type %q does not have a supported change key", recordType)
		}
		if recordType == "R" {
			return "RTE:R:" + field(2, 7) + ":" + field(7, 15), recordType, "", nil
		}
		return "RTE:L:" + field(2, 7) + ":" + field(7, 15) + ":" + field(15, 18) + ":" + field(18, 22), recordType, "", nil
	case "TOC":
		if len(line) < 4 {
			return "", "", "", fmt.Errorf("TOC record has length %d; expected at least 4", len(line))
		}
		recordType = string(line[0])
		switch recordType {
		case "T":
			return "TOC:T:" + field(1, 3), recordType, "", nil
		case "F":
			return "TOC:F:" + field(1, 4), recordType, "", nil
		default:
			return "", "", "", fmt.Errorf("unsupported TOC record type %q", recordType)
		}
	case "TVL":
		if len(line) < 10 {
			return "", "", "", fmt.Errorf("TVL record has length %d; expected at least 10", len(line))
		}
		return "TVL:" + field(0, 2) + ":" + field(2, 10), "TVL", "", nil
	case "RLC":
		if len(line) < 11 {
			return "", "", "", fmt.Errorf("RLC record has length %d; expected at least 11", len(line))
		}
		return "RLC:" + field(0, 3) + ":" + field(3, 11), "RLC", "", nil
	case "RCM":
		if len(line) < 14 {
			return "", "", "", fmt.Errorf("RCM record has length %d; expected at least 14", len(line))
		}
		return "RCM:" + field(0, 3) + ":" + field(3, 6) + ":" + field(6, 14), "RCM", "", nil
	case "TAP":
		if len(line) != 37 {
			return "", "", "", fmt.Errorf("TAP record has length %d; expected 37", len(line))
		}
		return "TAP:" + string(line), "TAP", "", nil
	case "TSP":
		if len(line) != 68 {
			return "", "", "", fmt.Errorf("TSP record has length %d; expected 68", len(line))
		}
		return "TSP:" + string(line), "TSP", "", nil
	case "DIS":
		if len(line) < 12 {
			return "", "", "", fmt.Errorf("DIS record has length %d; expected at least 12", len(line))
		}
		recordType = string(line[0])
		switch recordType {
		case "S":
			return "DIS:S:" + field(1, 4) + ":" + field(4, 12), recordType, "", nil
		case "D":
			if len(line) < 14 {
				return "", "", "", fmt.Errorf("DIS D record has length %d; expected at least 14", len(line))
			}
			return "DIS:D:" + field(1, 4) + ":" + field(4, 12) + ":" + field(12, 14), recordType, "", nil
		default:
			return "", "", "", fmt.Errorf("unsupported DIS record type %q", recordType)
		}
	case "RST":
		if len(line) < 4 {
			return "", "", "", fmt.Errorf("RST record has length %d; expected at least 4", len(line))
		}
		recordType = strings.TrimSpace(string(line[1:3]))
		cf := field(3, 4)
		code := field(4, 6)
		key := "RST:" + recordType + ":"
		switch recordType {
		case "RD":
			if len(line) < 20 {
				return "", "", "", fmt.Errorf("RST RD record has length %d; expected at least 20", len(line))
			}
			key += cf
		case "RH":
			if len(line) < 6 {
				return "", "", "", fmt.Errorf("RST RH record has length %d; expected at least 6", len(line))
			}
			key += cf + ":" + code
		case "HD":
			if len(line) < 21 {
				return "", "", "", fmt.Errorf("RST HD record has length %d; expected at least 21", len(line))
			}
			key += cf + ":" + code + ":" + field(6, 14)
		case "HL", "HC":
			if len(line) < 9 {
				return "", "", "", fmt.Errorf("RST %s record has length %d; expected at least 9", recordType, len(line))
			}
			key += cf + ":" + code + ":" + field(6, 9)
		case "HA":
			if len(line) < 6 {
				return "", "", "", fmt.Errorf("RST HA record has length %d; expected at least 6", len(line))
			}
			key += cf + ":" + code
		case "TR":
			if len(line) < 11 {
				return "", "", "", fmt.Errorf("RST TR record has length %d; expected at least 11", len(line))
			}
			key += cf + ":" + code + ":" + field(6, 11)
		case "TD":
			if len(line) < 26 {
				return "", "", "", fmt.Errorf("RST TD record has length %d; expected at least 26", len(line))
			}
			key += cf + ":" + code + ":" + field(6, 19)
		case "TT", "TP", "TE":
			minimumLength := 11
			if recordType == "TT" {
				minimumLength = 13
			}
			if len(line) < minimumLength {
				return "", "", "", fmt.Errorf("RST %s record has length %d; expected at least %d", recordType, len(line), minimumLength)
			}
			end := 11
			if recordType == "TT" {
				end = 13
			}
			key += cf + ":" + string(line[4:end])
		case "SR":
			if len(line) < 13 {
				return "", "", "", fmt.Errorf("RST SR record has length %d; expected at least 13", len(line))
			}
			key += cf + ":" + string(line[4:13])
		case "SD":
			if len(line) < 28 {
				return "", "", "", fmt.Errorf("RST SD record has length %d; expected at least 28", len(line))
			}
			key += cf + ":" + string(line[4:21])
		case "SQ", "SP", "SE":
			if len(line) < 14 {
				return "", "", "", fmt.Errorf("RST %s record has length %d; expected at least 14", recordType, len(line))
			}
			end := 13
			if recordType == "SE" {
				end = 14
			}
			key += cf + ":" + string(line[4:end])
		case "RR":
			if len(line) < 11 {
				return "", "", "", fmt.Errorf("RST RR record has length %d; expected at least 11", len(line))
			}
			key += cf + ":" + string(line[4:11])
		case "EC":
			if len(line) < 5 {
				return "", "", "", fmt.Errorf("RST EC record has length %d; expected at least 5", len(line))
			}
			key += cf + ":" + field(4, 5)
		case "CA":
			if len(line) < 29 {
				return "", "", "", fmt.Errorf("RST CA record has length %d; expected at least 29", len(line))
			}
			key += cf + ":" + string(line[4:29])
		default:
			return "", "", "", fmt.Errorf("unsupported RST record type %q", recordType)
		}
		return key, recordType, "", nil
	default:
		return "", "", "", fmt.Errorf("unsupported RSPS5045 snapshot file type %q", fileType)
	}
}

func writeSourceRows(ctx context.Context, collection *mongo.Collection, writes []mongo.WriteModel, datasource *ctdf.DataSourceReference) error {
	if len(writes) == 0 {
		return nil
	}
	if err := ensureFareImportLease(ctx, datasource); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := collection.BulkWrite(ctx, writes, options.BulkWrite().SetOrdered(true)); err != nil {
		return fmt.Errorf("write RSPS5045 source snapshot rows: %w", err)
	}
	return nil
}

func buildSnapshotArchive(ctx context.Context, datasetID, fareVersionID, sequence string) (*os.File, error) {
	collection := database.GetCollection(sourceRecordCollection)
	file, err := os.CreateTemp("", "travigo-rsps5045-*.zip")
	if err != nil {
		return nil, fmt.Errorf("create merged RSPS5045 archive: %w", err)
	}
	failed := func(err error) (*os.File, error) {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	writer := zip.NewWriter(file)
	manifestNames := make([]string, 0, len(snapshotFileTypes))
	generated := time.Now().UTC().Format("02/01/2006")
	for _, fileType := range snapshotFileTypes {
		count, countErr := collection.CountDocuments(ctx, bson.M{"datasetid": datasetID, "fareversionid": fareVersionID, "filetype": fileType})
		if countErr != nil {
			_ = writer.Close()
			return failed(fmt.Errorf("count merged RSPS5045 %s records: %w", fileType, countErr))
		}
		if count == 0 {
			continue
		}
		memberName := "RJFAF" + sequence + "." + fileType
		member, createErr := writer.Create(memberName)
		if createErr != nil {
			_ = writer.Close()
			return failed(fmt.Errorf("create merged RSPS5045 %s member: %w", fileType, createErr))
		}
		if _, err := fmt.Fprintf(member, "/!! Start of file\n/!! Content type: %s\n/!! Sequence: %s\n/!! Records: %d\n/!! Generated: %s\n/!! Exporter: Travigo\n", fileType, sequence, count, generated); err != nil {
			_ = writer.Close()
			return failed(fmt.Errorf("write merged RSPS5045 %s header: %w", fileType, err))
		}
		cursor, findErr := collection.Find(ctx, bson.M{"datasetid": datasetID, "fareversionid": fareVersionID, "filetype": fileType}, options.Find().SetSort(bson.D{{Key: "sourcekey", Value: 1}}).SetBatchSize(writeBatchSize))
		if findErr != nil {
			_ = writer.Close()
			return failed(fmt.Errorf("read merged RSPS5045 %s rows: %w", fileType, findErr))
		}
		buffer := bufio.NewWriterSize(member, 64*1024)
		var rows int64
		fileStarted := time.Now()
		lastProgress := fileStarted
		log.Info().Str("dataset", datasetID).Str("file_type", fileType).Int64("records", count).Msg("Rebuilding RSPS5045 snapshot member")
		for cursor.Next(ctx) {
			var row sourceRecord
			if err := cursor.Decode(&row); err != nil {
				cursor.Close(ctx)
				_ = writer.Close()
				return failed(fmt.Errorf("decode merged RSPS5045 %s row: %w", fileType, err))
			}
			record := row.Record
			if snapshotMarkerTypes[fileType] && len(record) > 0 {
				record = "R" + record[1:]
			}
			if _, err := buffer.WriteString(record + "\n"); err != nil {
				cursor.Close(ctx)
				_ = writer.Close()
				return failed(fmt.Errorf("write merged RSPS5045 %s row: %w", fileType, err))
			}
			rows++
			if rspsProgressDue(rows, lastProgress) {
				log.Info().
					Str("dataset", datasetID).
					Str("file_type", fileType).
					Int64("records", rows).
					Int64("expected_records", count).
					Dur("elapsed", time.Since(fileStarted)).
					Msg("RSPS5045 snapshot rebuild progress")
				lastProgress = time.Now()
			}
		}
		cursorErr := cursor.Err()
		_ = cursor.Close(ctx)
		if cursorErr != nil {
			_ = writer.Close()
			return failed(fmt.Errorf("iterate merged RSPS5045 %s rows: %w", fileType, cursorErr))
		}
		if rows != count {
			_ = writer.Close()
			return failed(fmt.Errorf("merged RSPS5045 %s count changed while snapshot was read", fileType))
		}
		if err := buffer.Flush(); err != nil {
			_ = writer.Close()
			return failed(fmt.Errorf("flush merged RSPS5045 %s rows: %w", fileType, err))
		}
		if _, err := fmt.Fprintf(member, "/!! End of file (%s)\n", generated); err != nil {
			_ = writer.Close()
			return failed(fmt.Errorf("write merged RSPS5045 %s footer: %w", fileType, err))
		}
		manifestNames = append(manifestNames, memberName)
		log.Info().Str("dataset", datasetID).Str("file_type", fileType).Int64("records", rows).Dur("elapsed", time.Since(fileStarted)).Msg("Rebuilt RSPS5045 snapshot member")
	}
	manifestName := "RJFAF" + sequence + ".DAT"
	manifest, err := writer.Create(manifestName)
	if err != nil {
		_ = writer.Close()
		return failed(fmt.Errorf("create merged RSPS5045 manifest: %w", err))
	}
	if _, err := fmt.Fprintf(manifest, "/!! Start of file\n/!! Content type: DAT\n/!! Sequence: %s\n/!! Records: %d\n/!! Generated: %s\n/!! Exporter: Travigo\n", sequence, len(manifestNames), generated); err != nil {
		_ = writer.Close()
		return failed(fmt.Errorf("write merged RSPS5045 manifest header: %w", err))
	}
	for _, name := range manifestNames {
		if _, err := io.WriteString(manifest, name+"\n"); err != nil {
			_ = writer.Close()
			return failed(fmt.Errorf("write merged RSPS5045 manifest row: %w", err))
		}
	}
	if _, err := fmt.Fprintf(manifest, "/!! End of file (%s)\n", generated); err != nil {
		_ = writer.Close()
		return failed(fmt.Errorf("write merged RSPS5045 manifest footer: %w", err))
	}
	if err := writer.Close(); err != nil {
		return failed(fmt.Errorf("close merged RSPS5045 archive: %w", err))
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return failed(fmt.Errorf("rewind merged RSPS5045 archive: %w", err))
	}
	return file, nil
}
