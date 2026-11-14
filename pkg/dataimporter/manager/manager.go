package manager

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/dataimporter/formats"
	"github.com/travigo/travigo/pkg/dataimporter/formats/cif"
	"github.com/travigo/travigo/pkg/dataimporter/formats/gtfs"
	"github.com/travigo/travigo/pkg/dataimporter/formats/naptan"
	"github.com/travigo/travigo/pkg/dataimporter/formats/nationalrailtoc"
	networkrailcorpus "github.com/travigo/travigo/pkg/dataimporter/formats/networkrail-corpus"
	"github.com/travigo/travigo/pkg/dataimporter/formats/siri_sx"
	"github.com/travigo/travigo/pkg/dataimporter/formats/siri_vm"
	"github.com/travigo/travigo/pkg/dataimporter/formats/transxchange"
	"github.com/travigo/travigo/pkg/dataimporter/formats/travelinenoc"
	"github.com/travigo/travigo/pkg/dataimporter/osmrailtracks"
	"github.com/travigo/travigo/pkg/dataimporter/tfltracks"
	"github.com/travigo/travigo/pkg/datasetversion"
	"github.com/travigo/travigo/pkg/journeytracks"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
)

func GetDatasource(identifier string) (datasets.DataSource, error) {
	registered := GetRegisteredDataSources()

	for _, datasource := range registered {
		if datasource.Identifier == identifier {
			return datasource, nil
		}
	}

	return datasets.DataSource{}, errors.New("Dataset could not be found")
}

func GetDataset(identifier string) (datasets.DataSet, error) {
	registered := GetRegisteredDataSets()

	for _, dataset := range registered {
		if dataset.Identifier == identifier {
			log.Info().
				Str("identifier", dataset.Identifier).
				Str("format", string(dataset.Format)).
				Str("provider", dataset.Provider.Name).
				Interface("supports", dataset.SupportedObjects).
				Msg("Found dataset")

			return dataset, nil
		}
	}

	return datasets.DataSet{}, errors.New("Dataset could not be found")
}

func createDatasetFormat(dataset *datasets.DataSet) (formats.Format, error) {
	var format formats.Format

	switch dataset.Format {
	case datasets.DataSetFormatTravelineNOC:
		format = &travelinenoc.TravelineData{}
	case datasets.DataSetFormatNaPTAN:
		format = &naptan.NaPTAN{}
	case datasets.DataSetFormatNationalRailTOC:
		format = &nationalrailtoc.TrainOperatingCompanyList{}
	case datasets.DataSetFormatNetworkRailCorpus:
		format = &networkrailcorpus.Corpus{}
	case datasets.DataSetFormatSiriVM:
		format = &siri_vm.SiriVM{}
	case datasets.DataSetFormatSiriSX:
		format = &siri_sx.SiriSX{}
	case datasets.DataSetFormatGTFSSchedule:
		format = &gtfs.Schedule{}
	case datasets.DataSetFormatGTFSRealtime:
		format = &gtfs.Realtime{}
	case datasets.DataSetFormatCIF:
		format = &cif.CommonInterfaceFormat{}
	case datasets.DataSetFormatTransXChange:
		format = &transxchange.TransXChange{}
	case datasets.DataSetFormatTfLRouteTracks:
		format = &tfltracks.Format{}
	case datasets.DataSetFormatOSMRailTracks:
		format = &osmrailtracks.Format{}
	default:
		return nil, errors.New(fmt.Sprintf("Unrecognised format %s", dataset.Format))
	}

	if dataset.ImportDestination == datasets.ImportDestinationSpecificRunner {
		return nil, errors.New("Custom runner required for this dataset, cannot import")
	}

	if dataset.ImportDestination == datasets.ImportDestinationRealtimeQueue {
		if dataset.Queue == nil {
			realtimeQueue, err := redis_client.QueueConnection.OpenQueue("realtime-queue")
			if err != nil {
				log.Fatal().Err(err).Msg("Failed to start redis realtime-queue")
			}
			dataset.Queue = &realtimeQueue
		}

		var realtimeQueueFormat formats.RealtimeQueueFormat
		realtimeQueueFormat = format.(formats.RealtimeQueueFormat)

		realtimeQueueFormat.SetupRealtimeQueue(*dataset.Queue)
	}

	return format, nil
}

func ImportDataset(dataset *datasets.DataSet, forceImport bool, skipCleanup bool) error {
	return importDataset(dataset, forceImport, skipCleanup, "", "")
}

func importDataset(dataset *datasets.DataSet, forceImport bool, skipCleanup bool, sourceOverride string, overrideETag string) error {
	importStartedAt := time.Now()
	datasetVersionCollection := database.GetCollection("dataset_versions")
	datasetImportReportCollection := database.GetCollection("dataset_import_reports")

	var existingDatasetVersion *ctdf.DatasetVersion
	datasetVersionCollection.FindOne(context.Background(), bson.M{"dataset": dataset.Identifier}).Decode(&existingDatasetVersion)

	if shouldSkipFreshDataset(dataset, existingDatasetVersion, forceImport, importStartedAt) {
		log.Info().
			Str("dataset", dataset.Identifier).
			Time("last_modified", existingDatasetVersion.LastModified).
			Dur("refresh_interval", dataset.RefreshInterval).
			Msg("Dataset version is still fresh, skipping import")
		return nil
	}

	var existingEtag string
	if existingDatasetVersion != nil && !forceImport {
		log.Info().Interface("version", existingDatasetVersion).Msg("Existing dataset version found")
		existingEtag = existingDatasetVersion.ETag
	}

	// Performance Review [Set H] [PR063] [P1] (batch): The second-resolution timestamp is also the
	// generation marker used by cleanup. Overlapping imports of one dataset can
	// delete each other's rows, while runs starting in the same second are not
	// distinguishable. Before parallelising datasets/retries, enforce per-dataset
	// ownership and a unique fenced generation; publish only after every batch is
	// complete, then retire the previous generation without touching an active run.
	datasource := &ctdf.DataSourceReference{
		OriginalFormat: string(dataset.Format),
		ProviderName:   dataset.Provider.Name,
		ProviderID:     dataset.DataSourceRef,
		DatasetID:      dataset.Identifier,
		Timestamp:      fmt.Sprintf("%d", time.Now().Unix()),
	}

	format, err := createDatasetFormat(dataset)
	if err != nil {
		return err
	}
	if apiFormat, ok := format.(formats.APIFormat); ok {
		importReport, err := apiFormat.ImportAPI(*dataset, datasource)
		if err != nil {
			return err
		}
		if dataset.SupportedObjects.JourneyTracks {
			if err := applyJourneyTracks(dataset, datasource, !skipCleanup); err != nil {
				return err
			}
		}
		importReport = filterImportReport(importReport, dataset.SupportedObjects)
		importReport.DatasetIdentifier = dataset.Identifier
		importReport.CreationDateTime = time.Now()
		importReport.RunTime = time.Since(importStartedAt)
		if _, err := datasetImportReportCollection.InsertOne(context.Background(), importReport); err != nil {
			return err
		}
		return datasetversion.Upsert(context.Background(), ctdf.DatasetVersion{
			Dataset: dataset.Identifier, LastModified: time.Now(),
		})
	}

	source := dataset.Source
	var etag string
	if sourceOverride != "" {
		source, etag = sourceOverride, overrideETag
	}

	if sourceOverride == "" && isValidUrl(dataset.Source) {
		var tempFile *os.File
		var hasChanged bool
		var err error
		hasChanged, tempFile, etag, err = tempDownloadFile(dataset, existingEtag)
		if err != nil {
			return err
		}

		if !hasChanged {
			log.Info().Str("dataset", dataset.Identifier).Msg("File ETag is not new, skipping processing")
			return nil
		}

		source = tempFile.Name()
		defer os.Remove(tempFile.Name())
		defer tempFile.Close()
	}

	// Calculate the hash of the file
	// Performance Review [Set D] [PR064] [P2] (batch): A downloaded file is written to disk, read
	// fully again for SHA-256, then reopened for parsing; GTFS adds another archive
	// copy. Hash remote bytes during the checked download and reuse a seekable file
	// for parsing. Keep conditional HTTP requests and the local-file hash path, and
	// reject read errors instead of treating a partial digest as a valid version.
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()

	hash := sha256.New()
	sourceSize, err := io.Copy(hash, f)
	if err != nil {
		return fmt.Errorf("calculate file hash: %w", err)
	}
	sourceFileHash := hex.EncodeToString(hash.Sum(nil))

	// Check if the file hasn't changed
	if existingDatasetVersion != nil && existingDatasetVersion.Hash == sourceFileHash && !forceImport {
		log.Info().Str("dataset", dataset.Identifier).Msg("File hash is not new, skipping processing")
		return nil
	}

	var datasetProvidedVersion string
	if hasPublisherGTFSVersion(dataset) {
		datasetProvidedVersion = gtfs.ReadScheduleFeedVersion(f, sourceSize)
		if !forceImport && existingDatasetVersion != nil && datasetProvidedVersion != "" &&
			datasetProvidedVersion == existingDatasetVersion.DatasetProvidedVersion {
			log.Info().Str("dataset", dataset.Identifier).
				Str("dataset_provided_version", datasetProvidedVersion).
				Msg("GTFS feed version is unchanged, skipping processing")
			return nil
		}
	}

	// Parse the file
	sourceFileReaders := []io.Reader{}

	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()

	switch dataset.UnpackBundle {
	case datasets.BundleFormatNone, "":
		sourceFileReaders = append(sourceFileReaders, file)
	case datasets.BundleFormatGZ:
		gzipDecoder, err := gzip.NewReader(file)
		if err != nil {
			log.Fatal().Err(err).Msg("cannot decode gzip stream")
		}
		defer gzipDecoder.Close()

		sourceFileReaders = append(sourceFileReaders, gzipDecoder)
	case datasets.BundleFormatZIP:
		archive, err := zip.OpenReader(source)
		if err != nil {
			return err
		}
		defer archive.Close()

		// Performance Review [Set D] [PR065] [P2] (batch): Every archive member is opened before
		// the first is parsed, and deferred closes retain all decompression readers
		// until the whole bundle finishes. Iterate supported, non-directory members
		// one at a time and close immediately after import, with per-entry/total
		// expanded-byte limits. Keep one run marker across members and only clean up
		// after the complete bundle succeeds; do not publish each file independently.
		for i, zipFile := range archive.File {
			zipFileOpen, err := zipFile.Open()
			if err != nil {
				log.Fatal().Err(err).Msg("Failed to open file")
			}
			defer zipFileOpen.Close()

			sourceFileReaders = append(sourceFileReaders, zipFileOpen)

			log.Debug().Int("index", i).Str("path", zipFile.Name).Msg("Storing zip file")
		}
	default:
		return errors.New(fmt.Sprintf("Cannot handle bundle format %s", dataset.UnpackBundle))
	}

	importReports := make([]datasets.DataImportReport, len(sourceFileReaders))
	for i, sourceFileReader := range sourceFileReaders {
		format, err := createDatasetFormat(dataset)
		if err != nil {
			return err
		}

		// Actually import it
		err = format.ParseFile(sourceFileReader)
		if err != nil {
			return err
		}

		log.Debug().Int("index", i).Msg("Opening zipped file")
		importReport, err := format.Import(*dataset, datasource)
		if err != nil {
			return err
		}
		importReports[i] = importReport
	}

	if dataset.SupportedObjects.JourneyTracks {
		if err := applyJourneyTracks(dataset, datasource, !skipCleanup); err != nil {
			return err
		}
	}
	if !skipCleanup {
		if dataset.SupportedObjects.Stops {
			cleanupOldRecords("stops_raw", datasource)
		}
		if dataset.SupportedObjects.StopGroups {
			cleanupOldRecords("stop_groups", datasource)
		}
		if dataset.SupportedObjects.Operators {
			cleanupOldRecords("operators", datasource)
		}
		if dataset.SupportedObjects.OperatorGroups {
			cleanupOldRecords("operator_groups", datasource)
		}
		if dataset.SupportedObjects.Services {
			cleanupOldRecords("services_raw", datasource)
		}
		if dataset.SupportedObjects.Journeys {
			cleanupOldRecords(database.JourneysRawCollectionName, datasource)
			if dataset.Format != datasets.DataSetFormatOSMRailTracks {
				if err := journeytracks.CleanupStaleUnreferencedTracks(context.Background(), datasource); err != nil {
					return fmt.Errorf("clean stale journey tracks: %w", err)
				}
			}
		}
	}

	// Update dataset version
	// Performance Review [Set H] [PR066] [P1] (batch): Cleanup errors are discarded, and both the
	// version upsert and report insert errors below are overwritten/ignored before
	// returning nil. A failed cleanup can therefore be recorded as a completed hash
	// and skipped next time. Propagate every phase's outcome; persist resumable phase
	// state or a validated generation so retrying finalisation does not require a
	// full reimport or expose a partially written dataset as complete.
	if dataset.ImportDestination != datasets.ImportDestinationRealtimeQueue {
		datasetVersion := ctdf.DatasetVersion{
			Dataset:                dataset.Identifier,
			Hash:                   sourceFileHash,
			ETag:                   etag,
			LastModified:           time.Now(),
			DatasetProvidedVersion: datasetProvidedVersion,
		}

		if err := datasetversion.Upsert(context.Background(), datasetVersion); err != nil {
			return err
		}

		// Aggregate the import report
		importReport := datasets.DataImportReport{
			DatasetIdentifier:      dataset.Identifier,
			DatasetProvidedVersion: datasetProvidedVersion,
			CreationDateTime:       time.Now(),
			RunTime:                time.Since(importStartedAt),
		}

		for _, report := range importReports {
			report = filterImportReport(report, dataset.SupportedObjects)

			importReport.ImportedStops += report.ImportedStops
			importReport.ImportedStopGroups += report.ImportedStopGroups
			importReport.ImportedServices += report.ImportedServices
			importReport.ImportedJourneys += report.ImportedJourneys
			importReport.ImportedJourneyTracks += report.ImportedJourneyTracks
			importReport.ImportedOperators += report.ImportedOperators
			importReport.ImportedOperationGroups += report.ImportedOperationGroups
		}

		_, err := datasetImportReportCollection.InsertOne(context.Background(), importReport)
		return err
	}

	return nil
}

func shouldSkipFreshDataset(dataset *datasets.DataSet, version *ctdf.DatasetVersion, forceImport bool, now time.Time) bool {
	if forceImport || version == nil || dataset.RefreshInterval <= 0 || version.LastModified.IsZero() {
		return false
	}
	return version.LastModified.Add(dataset.RefreshInterval).After(now)
}

func applyJourneyTracks(dataset *datasets.DataSet, datasource *ctdf.DataSourceReference, cleanup bool) error {
	var err error
	if dataset.Format == datasets.DataSetFormatTfLRouteTracks {
		err = journeytracks.ApplyDatasetForOperator(context.Background(), datasource.DatasetID, datasource.Timestamp, tfltracks.TfLOperatorRef)
	} else if dataset.Format == datasets.DataSetFormatOSMRailTracks {
		err = journeytracks.ApplyOSMRailDataset(context.Background(), datasource.DatasetID, datasource.Timestamp)
	} else {
		err = journeytracks.ApplyDataset(context.Background(), datasource.DatasetID, datasource.Timestamp)
	}
	if err != nil {
		return err
	}
	if cleanup {
		cleanupOldRecords(journeytracks.RouteCollectionName, datasource)
		if dataset.Format == datasets.DataSetFormatOSMRailTracks {
			return journeytracks.CleanupOSMRailTracks(context.Background(), datasource)
		}
		return journeytracks.CleanupStaleUnreferencedTracks(context.Background(), datasource)
	}
	return nil
}

func filterImportReport(report datasets.DataImportReport, supported datasets.SupportedObjects) datasets.DataImportReport {
	if !supported.Stops {
		report.ImportedStops = 0
	}
	if !supported.StopGroups {
		report.ImportedStopGroups = 0
	}
	if !supported.Services {
		report.ImportedServices = 0
	}
	if !supported.Journeys {
		report.ImportedJourneys = 0
	}
	if !supported.JourneyTracks {
		report.ImportedJourneyTracks = 0
	}
	if !supported.Operators {
		report.ImportedOperators = 0
	}
	if !supported.OperatorGroups {
		report.ImportedOperationGroups = 0
	}

	return report
}

func isValidUrl(toTest string) bool {
	_, err := url.ParseRequestURI(toTest)
	if err != nil {
		return false
	}

	u, err := url.Parse(toTest)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}

	return true
}

func cleanupOldRecords(collectionName string, datasource *ctdf.DataSourceReference) {
	// Performance Review [Set O] [PR069] [P2] (batch): This runs once per supported collection for
	// every changed dataset. Declared journeys_raw and journey_tracks indexes cover this predicate,
	// but stops_raw lacks a datasource cleanup index, so repeated stop imports can scan unrelated
	// rows. Check actual indexes/explain plans; add an equality-prefix index for format/dataset plus
	// generation where justified, or retire staged generations in bounded batches. Measure deletion
	// write cost.
	collection := database.GetCollection(collectionName)

	query := bson.M{
		"$and": bson.A{
			bson.M{"datasource.originalformat": datasource.OriginalFormat},
			bson.M{"datasource.datasetid": datasource.DatasetID},
			bson.M{"datasource.timestamp": bson.M{
				"$ne": datasource.Timestamp,
			}},
		},
	}

	result, _ := collection.DeleteMany(context.Background(), query)

	if result != nil {
		log.Info().
			Str("collection", collectionName).
			Int64("num", result.DeletedCount).
			Msg("Cleaned up old records")
	}
}
