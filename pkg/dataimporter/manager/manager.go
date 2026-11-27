package manager

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/rand"
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
	"github.com/travigo/travigo/pkg/dataimporter/formats/netex"
	networkrailcorpus "github.com/travigo/travigo/pkg/dataimporter/formats/networkrail-corpus"
	"github.com/travigo/travigo/pkg/dataimporter/formats/rsps5045"
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
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
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
	case datasets.DataSetFormatNetExFares:
		format = &netex.Fares{}
	case datasets.DataSetFormatNationalRailFares:
		format = &rsps5045.Fares{}
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
	var fareLease *fareActivationLease
	if dataset.SupportedObjects.Fares {
		lease, err := acquireFareActivationLease(dataset.Identifier)
		if err != nil {
			return fmt.Errorf("acquire fare import lease for %s: %w", dataset.Identifier, err)
		}
		fareLease = lease
		defer fareLease.release()
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
		FareVersionID:  fmt.Sprintf("%s-%d", dataset.Identifier, time.Now().UnixNano()),
	}
	stagingFareVersionRegistered := false
	registerFareVersionForImport := func() error {
		if fareLease == nil || stagingFareVersionRegistered {
			return nil
		}
		if err := fareLease.prepareForFareImport(); err != nil {
			return fmt.Errorf("prepare fare-version cleanup for %s: %w", dataset.Identifier, err)
		}
		if err := setStagingFareVersion(fareLease, datasource.FareVersionID); err != nil {
			return fmt.Errorf("register staged fare version for %s: %w", dataset.Identifier, err)
		}
		stagingFareVersionRegistered = true
		return nil
	}
	if fareLease != nil {
		datasource.ImportContext = fareLease.ctx
		datasource.FareLeaseOwner = fareLease.owner
		datasource.FareActivationFence = fareLease.fence
	}

	format, err := createDatasetFormat(dataset)
	if err != nil {
		return err
	}
	if apiFormat, ok := format.(formats.APIFormat); ok {
		if err := registerFareVersionForImport(); err != nil {
			return err
		}
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
		if importReport.FareDataPresent {
			if err := activateFareVersionForImport(dataset, datasource, fareLease); err != nil {
				return err
			}
		} else if fareLease != nil {
			if err := discardStagingFareVersion(fareLease, datasource.FareVersionID); err != nil {
				return err
			}
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

	archiveFormat, err := createDatasetFormat(dataset)
	if err != nil {
		return err
	}
	if archiveImporter, ok := archiveFormat.(formats.ArchiveFormat); ok {
		archiveInfo, err := file.Stat()
		if err != nil {
			return fmt.Errorf("stat fare archive: %w", err)
		}
		if err := archiveImporter.ParseArchive(file, archiveInfo.Size()); err != nil {
			return err
		}
		if err := registerFareVersionForImport(); err != nil {
			return err
		}
		importReport, err := archiveImporter.Import(*dataset, datasource)
		if err != nil {
			return err
		}
		importReport = filterImportReport(importReport, dataset.SupportedObjects)
		importReport.DatasetIdentifier = dataset.Identifier
		importReport.CreationDateTime = time.Now()
		importReport.RunTime = time.Since(importStartedAt)
		if importReport.FareDataPresent {
			if err := activateFareVersionForImport(dataset, datasource, fareLease); err != nil {
				return err
			}
		} else if fareLease != nil {
			if err := discardStagingFareVersion(fareLease, datasource.FareVersionID); err != nil {
				return err
			}
		}
		if err := datasetversion.Upsert(context.Background(), ctdf.DatasetVersion{
			Dataset:                dataset.Identifier,
			Hash:                   sourceFileHash,
			ETag:                   etag,
			LastModified:           time.Now(),
			DatasetProvidedVersion: datasetProvidedVersion,
		}); err != nil {
			return err
		}
		_, err = datasetImportReportCollection.InsertOne(context.Background(), importReport)
		return err
	}

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
		if err := registerFareVersionForImport(); err != nil {
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
			importReport.FareDataPresent = importReport.FareDataPresent || report.FareDataPresent
			importReport.ImportedFareLocations += report.ImportedFareLocations
			importReport.FareLocationsLinkedToStops += report.FareLocationsLinkedToStops
			importReport.FareLocationsWithoutStopLink += report.FareLocationsWithoutStopLink
			importReport.ImportedFareProducts += report.ImportedFareProducts
			importReport.ImportedFareOffers += report.ImportedFareOffers
			importReport.ImportedFareRules += report.ImportedFareRules
			importReport.ImportedFareMedia += report.ImportedFareMedia
			importReport.ImportedFareRiderCategories += report.ImportedFareRiderCategories
			importReport.ImportedFareTariffs += report.ImportedFareTariffs
			importReport.ImportedFareNetworks += report.ImportedFareNetworks
			importReport.ImportedFareRouteNetworks += report.ImportedFareRouteNetworks
			importReport.ImportedFareTimeframes += report.ImportedFareTimeframes
			importReport.ImportedFareLegJoins += report.ImportedFareLegJoins
			importReport.ImportedFareTransferRules += report.ImportedFareTransferRules
			importReport.MergeFareValidityGaps(report)
		}
		if dataset.Format == datasets.DataSetFormatGTFSSchedule && dataset.SupportedObjects.Fares && !importReport.FareDataPresent {
			if err := retireFareVersionForImport(dataset, fareLease); err != nil {
				return err
			}
		} else if importReport.FareDataPresent {
			if err := activateFareVersionForImport(dataset, datasource, fareLease); err != nil {
				return err
			}
		} else if fareLease != nil {
			if err := discardStagingFareVersion(fareLease, datasource.FareVersionID); err != nil {
				return err
			}
		}
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

		_, err := datasetImportReportCollection.InsertOne(context.Background(), importReport)
		return err
	}

	return nil
}

func activateFareVersion(dataset *datasets.DataSet, datasource *ctdf.DataSourceReference) error {
	lease, err := acquireFareActivationLease(dataset.Identifier)
	if err != nil {
		return fmt.Errorf("acquire fare activation lease for %s: %w", dataset.Identifier, err)
	}
	defer lease.release()
	return activateFareVersionWithLease(dataset, datasource, lease)
}

func activateFareVersionForImport(dataset *datasets.DataSet, datasource *ctdf.DataSourceReference, lease *fareActivationLease) error {
	if lease == nil {
		return activateFareVersion(dataset, datasource)
	}
	return activateFareVersionWithLease(dataset, datasource, lease)
}

func activateFareVersionWithLease(dataset *datasets.DataSet, datasource *ctdf.DataSourceReference, lease *fareActivationLease) error {
	if datasource == nil || datasource.FareVersionID == "" {
		return fmt.Errorf("cannot activate fares without an immutable fare version ID")
	}
	if lease == nil || lease.datasetID != dataset.Identifier || lease.ctx.Err() != nil {
		return fmt.Errorf("cannot activate fare version without the live dataset lease")
	}
	if err := validateFareVersion(dataset.Identifier, datasource.FareVersionID); err != nil {
		return fmt.Errorf("validate fare version for %s: %w", dataset.Identifier, err)
	}
	linkedScheduleDatasetID := dataset.Identifier
	if dataset.LinkedDataset != "" {
		linkedScheduleDatasetID = dataset.LinkedDataset
	} else if dataset.Format == datasets.DataSetFormatNationalRailFares {
		linkedScheduleDatasetID = "gb-nationalrail-timetable"
	}
	var previous ctdf.FareVersionHead
	previousErr := database.GetCollection("fare_versions").FindOne(lease.ctx, bson.M{"datasetid": dataset.Identifier}).Decode(&previous)
	if previousErr != nil && previousErr != mongo.ErrNoDocuments {
		return fmt.Errorf("read active fare version for %s: %w", dataset.Identifier, previousErr)
	}
	now := time.Now()
	update := bson.M{"$set": bson.M{
		"activeversionid":         datasource.FareVersionID,
		"previousversionid":       previous.ActiveVersionID,
		"sourcesequence":          datasource.FareSourceSequence,
		"linkedscheduledatasetid": linkedScheduleDatasetID,
		"activatedat":             now,
		"leaseexpiresat":          now.Add(fareActivationLeaseDuration),
	}, "$unset": bson.M{"stagingfareversionid": ""}}
	if retiredID := previous.PreviousVersionID; retiredID != "" && retiredID != datasource.FareVersionID && retiredID != previous.ActiveVersionID {
		update["$addToSet"] = bson.M{"pendingretiredversionids": retiredID}
	}
	result, err := database.GetCollection("fare_versions").UpdateOne(
		lease.ctx,
		bson.M{"datasetid": dataset.Identifier, "leaseowner": lease.owner, "activationfence": lease.fence, "leaseexpiresat": bson.M{"$gt": now}},
		update,
	)
	if err != nil {
		return fmt.Errorf("activate fare version for %s: %w", dataset.Identifier, err)
	}
	if result.MatchedCount != 1 {
		return fmt.Errorf("activate fare version for %s: dataset lease expired or was superseded", dataset.Identifier)
	}
	if err := lease.ctx.Err(); err != nil {
		return fmt.Errorf("fare activation lease for %s was lost before cleanup: %w", dataset.Identifier, err)
	}
	if err := lease.cleanupPendingFareVersions(); err != nil {
		if lease.ctx.Err() != nil {
			return fmt.Errorf("fare activation lease for %s was lost during cleanup: %w", dataset.Identifier, lease.ctx.Err())
		}
		log.Warn().Err(err).Str("dataset", dataset.Identifier).Msg("Could not remove all retired fare versions; cleanup will retry on a later import")
	}
	if err := lease.ctx.Err(); err != nil {
		return fmt.Errorf("fare activation lease for %s was lost during cleanup: %w", dataset.Identifier, err)
	}
	return nil
}

func retireFareVersionForImport(dataset *datasets.DataSet, lease *fareActivationLease) error {
	if lease == nil || lease.datasetID != dataset.Identifier || lease.ctx.Err() != nil {
		return fmt.Errorf("cannot retire fare version without the live dataset lease")
	}
	var previous ctdf.FareVersionHead
	err := database.GetCollection("fare_versions").FindOne(lease.ctx, bson.M{"datasetid": dataset.Identifier}).Decode(&previous)
	if err != nil && err != mongo.ErrNoDocuments {
		return fmt.Errorf("read active fare version for %s: %w", dataset.Identifier, err)
	}
	now := time.Now()
	update := bson.M{"$unset": bson.M{
		"activeversionid": "", "previousversionid": "", "sourcesequence": "",
		"linkedscheduledatasetid": "", "activatedat": "", "stagingfareversionid": "",
	}, "$set": bson.M{"leaseexpiresat": now.Add(fareActivationLeaseDuration)}}
	retired := uniqueFareVersionIDs(previous.ActiveVersionID, previous.PreviousVersionID)
	if len(retired) > 0 {
		update["$addToSet"] = bson.M{"pendingretiredversionids": bson.M{"$each": retired}}
	}
	result, err := database.GetCollection("fare_versions").UpdateOne(lease.ctx,
		bson.M{"datasetid": dataset.Identifier, "leaseowner": lease.owner, "activationfence": lease.fence, "leaseexpiresat": bson.M{"$gt": now}},
		update,
	)
	if err != nil {
		return fmt.Errorf("retire fare catalogue for %s: %w", dataset.Identifier, err)
	}
	if result.MatchedCount != 1 {
		return fmt.Errorf("retire fare catalogue for %s: dataset lease expired or was superseded", dataset.Identifier)
	}
	if err := lease.cleanupPendingFareVersions(); err != nil {
		if lease.ctx.Err() != nil {
			return fmt.Errorf("fare retirement lease for %s was lost during cleanup: %w", dataset.Identifier, lease.ctx.Err())
		}
		return err
	}
	return nil
}

func uniqueFareVersionIDs(values ...string) []string {
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

const (
	fareActivationLeaseDuration    = 5 * time.Minute
	fareActivationHeartbeat        = 5 * time.Second
	fareActivationHeartbeatTimeout = 2 * time.Second
)

type fareActivationLease struct {
	datasetID              string
	owner                  string
	fence                  int64
	ctx                    context.Context
	cancel                 context.CancelFunc
	done                   chan struct{}
	abandonedStagingQueued bool
}

func acquireFareActivationLease(datasetID string) (*fareActivationLease, error) {
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, fmt.Errorf("create lease token: %w", err)
	}
	owner := fmt.Sprintf("%x", token[:])
	collection := database.GetCollection("fare_versions")
	acquireCtx, cancelAcquire := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelAcquire()
	for {
		now := time.Now()
		filter := bson.M{"datasetid": datasetID, "$or": bson.A{
			bson.M{"leaseexpiresat": bson.M{"$lte": now}},
			bson.M{"leaseexpiresat": bson.M{"$exists": false}},
			bson.M{"leaseowner": bson.M{"$exists": false}},
		}}
		update := bson.M{
			"$set": bson.M{"leaseowner": owner, "leaseexpiresat": now.Add(fareActivationLeaseDuration)},
			"$inc": bson.M{"activationfence": 1},
		}
		var acquired ctdf.FareVersionHead
		err := collection.FindOneAndUpdate(acquireCtx, filter, update,
			options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&acquired)
		if err == nil {
			leaseCtx, cancelLease := context.WithCancel(context.Background())
			abandonedStaging := acquired.StagingFareVersionID != "" && acquired.StagingFareVersionID != acquired.ActiveVersionID
			lease := &fareActivationLease{datasetID: datasetID, owner: owner, fence: acquired.ActivationFence, ctx: leaseCtx, cancel: cancelLease, done: make(chan struct{}), abandonedStagingQueued: abandonedStaging}
			go lease.heartbeat()
			if err := lease.reconcileStagingFareVersion(acquired.StagingFareVersionID, acquired.ActiveVersionID); err != nil {
				lease.release()
				return nil, fmt.Errorf("reconcile staged fare version for %s: %w", datasetID, err)
			}
			return lease, nil
		}
		if !mongo.IsDuplicateKeyError(err) && err != mongo.ErrNoDocuments {
			return nil, err
		}
		wait := time.NewTimer(250 * time.Millisecond)
		select {
		case <-acquireCtx.Done():
			wait.Stop()
			return nil, acquireCtx.Err()
		case <-wait.C:
		}
	}
}

func (lease *fareActivationLease) prepareForFareImport() error {
	if lease == nil || lease.ctx.Err() != nil {
		return fmt.Errorf("cannot prepare fare import without the live dataset lease")
	}
	if lease.abandonedStagingQueued {
		// Give the previous owner's heartbeat time to observe the new fence and
		// cancel its context before the first sweep. Durable tombstones catch any
		// write that was already in flight when ownership changed.
		timer := time.NewTimer(fareActivationHeartbeat + fareActivationHeartbeatTimeout)
		select {
		case <-lease.ctx.Done():
			timer.Stop()
			return fmt.Errorf("dataset lease was lost while waiting for the previous importer to stop")
		case <-timer.C:
		}
		lease.abandonedStagingQueued = false
	}
	return lease.cleanupPendingFareVersions()
}

func (lease *fareActivationLease) heartbeat() {
	defer close(lease.done)
	ticker := time.NewTicker(fareActivationHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-lease.ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			renewCtx, cancel := context.WithTimeout(lease.ctx, fareActivationHeartbeatTimeout)
			result, err := database.GetCollection("fare_versions").UpdateOne(renewCtx,
				bson.M{"datasetid": lease.datasetID, "leaseowner": lease.owner, "activationfence": lease.fence, "leaseexpiresat": bson.M{"$gt": now}},
				bson.M{"$set": bson.M{"leaseexpiresat": now.Add(fareActivationLeaseDuration)}},
			)
			cancel()
			if err != nil || result.MatchedCount != 1 {
				lease.cancel()
				return
			}
		}
	}
}

func (lease *fareActivationLease) release() {
	if lease.ctx.Err() == nil {
		var head ctdf.FareVersionHead
		if err := database.GetCollection("fare_versions").FindOne(lease.ctx, bson.M{"datasetid": lease.datasetID, "leaseowner": lease.owner, "activationfence": lease.fence}).Decode(&head); err == nil && head.StagingFareVersionID != "" {
			if err := lease.reconcileStagingFareVersion(head.StagingFareVersionID, head.ActiveVersionID); err != nil {
				log.Warn().Err(err).Str("dataset", lease.datasetID).Str("fare_version", head.StagingFareVersionID).Msg("Could not queue abandoned fare version for cleanup")
			} else if err := lease.cleanupPendingFareVersions(); err != nil {
				log.Warn().Err(err).Str("dataset", lease.datasetID).Msg("Could not clean abandoned fare version; cleanup will retry on a later import")
			}
		}
	}
	lease.cancel()
	<-lease.done
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = database.GetCollection("fare_versions").UpdateOne(ctx,
		bson.M{"datasetid": lease.datasetID, "leaseowner": lease.owner, "activationfence": lease.fence},
		bson.M{"$unset": bson.M{"leaseowner": "", "leaseexpiresat": ""}})
}

func setStagingFareVersion(lease *fareActivationLease, fareVersionID string) error {
	if lease == nil || fareVersionID == "" || lease.ctx.Err() != nil {
		return fmt.Errorf("cannot register staged fare version without the live dataset lease")
	}
	now := time.Now()
	result, err := database.GetCollection("fare_versions").UpdateOne(lease.ctx,
		bson.M{"datasetid": lease.datasetID, "leaseowner": lease.owner, "activationfence": lease.fence, "leaseexpiresat": bson.M{"$gt": now}},
		bson.M{"$set": bson.M{"stagingfareversionid": fareVersionID, "leaseexpiresat": now.Add(fareActivationLeaseDuration)}},
	)
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return fmt.Errorf("dataset lease expired or was superseded")
	}
	return nil
}

func discardStagingFareVersion(lease *fareActivationLease, fareVersionID string) error {
	if lease == nil || fareVersionID == "" || lease.ctx.Err() != nil {
		return fmt.Errorf("cannot discard staged fare version without the live dataset lease")
	}
	now := time.Now()
	result, err := database.GetCollection("fare_versions").UpdateOne(lease.ctx,
		bson.M{"datasetid": lease.datasetID, "leaseowner": lease.owner, "activationfence": lease.fence, "leaseexpiresat": bson.M{"$gt": now}, "stagingfareversionid": fareVersionID},
		bson.M{
			"$unset":    bson.M{"stagingfareversionid": ""},
			"$addToSet": bson.M{"pendingretiredversionids": fareVersionID},
			"$set":      bson.M{"leaseexpiresat": now.Add(fareActivationLeaseDuration)},
		},
	)
	if err != nil {
		return fmt.Errorf("queue unused fare version for cleanup: %w", err)
	}
	if result.MatchedCount == 0 {
		return nil
	}
	if err := lease.cleanupPendingFareVersions(); err != nil {
		return fmt.Errorf("clean unused fare version: %w", err)
	}
	return nil
}

func (lease *fareActivationLease) reconcileStagingFareVersion(stagingID, activeID string) error {
	if stagingID == "" {
		return nil
	}
	if lease == nil || lease.ctx.Err() != nil {
		return fmt.Errorf("cannot reconcile staged fare version without the live dataset lease")
	}
	now := time.Now()
	update := bson.M{"$unset": bson.M{"stagingfareversionid": ""}, "$set": bson.M{"leaseexpiresat": now.Add(fareActivationLeaseDuration)}}
	if stagingID != activeID {
		update["$addToSet"] = bson.M{"pendingretiredversionids": stagingID, "stagingcleanupversionids": stagingID}
	}
	result, err := database.GetCollection("fare_versions").UpdateOne(lease.ctx,
		bson.M{"datasetid": lease.datasetID, "leaseowner": lease.owner, "activationfence": lease.fence, "leaseexpiresat": bson.M{"$gt": now}, "stagingfareversionid": stagingID},
		update,
	)
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return fmt.Errorf("dataset lease expired, was superseded, or staged version changed")
	}
	return nil
}

func (lease *fareActivationLease) cleanupPendingFareVersions() error {
	if lease == nil || lease.ctx.Err() != nil {
		return fmt.Errorf("cannot clean retired fare versions without the live dataset lease")
	}
	var head ctdf.FareVersionHead
	if err := database.GetCollection("fare_versions").FindOne(lease.ctx,
		bson.M{"datasetid": lease.datasetID, "leaseowner": lease.owner, "activationfence": lease.fence, "leaseexpiresat": bson.M{"$gt": time.Now()}}).Decode(&head); err != nil {
		return fmt.Errorf("read pending fare versions: %w", err)
	}
	tombstones := uniqueFareVersionIDs(head.StagingCleanupVersionIDs...)
	for _, fareVersionID := range uniqueFareVersionIDs(append(head.PendingRetiredVersionIDs, tombstones...)...) {
		if fareVersionID == head.ActiveVersionID {
			now := time.Now()
			_, err := database.GetCollection("fare_versions").UpdateOne(lease.ctx,
				bson.M{"datasetid": lease.datasetID, "leaseowner": lease.owner, "activationfence": lease.fence, "leaseexpiresat": bson.M{"$gt": now}},
				bson.M{"$pull": bson.M{"pendingretiredversionids": fareVersionID}, "$set": bson.M{"leaseexpiresat": now.Add(fareActivationLeaseDuration)}},
			)
			if err != nil {
				return fmt.Errorf("remove active fare version from pending cleanup: %w", err)
			}
			continue
		}
		if err := deleteFareVersionRecords(lease.ctx, lease.datasetID, []string{fareVersionID}); err != nil {
			return err
		}
		now := time.Now()
		result, err := database.GetCollection("fare_versions").UpdateOne(lease.ctx,
			bson.M{"datasetid": lease.datasetID, "leaseowner": lease.owner, "activationfence": lease.fence, "leaseexpiresat": bson.M{"$gt": now}},
			bson.M{"$pull": bson.M{"pendingretiredversionids": fareVersionID}, "$set": bson.M{"leaseexpiresat": now.Add(fareActivationLeaseDuration)}},
		)
		if err != nil {
			return fmt.Errorf("remove cleaned fare version from pending list: %w", err)
		}
		if result.MatchedCount != 1 {
			return fmt.Errorf("dataset lease expired or was superseded while recording fare cleanup")
		}
	}
	return nil
}

func deleteFareVersionRecords(ctx context.Context, datasetID string, fareVersionIDs []string) error {
	if len(fareVersionIDs) == 0 {
		return nil
	}
	for _, collectionName := range []string{"fare_locations", "fare_tariffs", "fare_products", "fare_rider_categories", "fare_media", "fare_networks", "fare_route_networks", "fare_timeframes", "fare_offers", "fare_leg_rules", "fare_transfer_rules", "fare_effective_leg_joins", "fare_caps", "rsps5045_source_records"} {
		if _, err := database.GetCollection(collectionName).DeleteMany(ctx, bson.M{
			"datasetid": datasetID, "fareversionid": bson.M{"$in": fareVersionIDs},
		}); err != nil {
			return fmt.Errorf("delete retired fare records from %s: %w", collectionName, err)
		}
	}
	for _, collectionName := range []string{"rsps5045_flow_staging", "rsps5045_fare_staging"} {
		if _, err := database.GetCollection(collectionName).DeleteMany(ctx, bson.M{"runid": bson.M{"$in": fareVersionIDs}}); err != nil {
			return fmt.Errorf("delete retired fare staging records from %s: %w", collectionName, err)
		}
	}
	return nil
}

func validateFareVersion(datasetID, fareVersionID string) error {
	filter := bson.M{"datasetid": datasetID, "fareversionid": fareVersionID}
	ctx := context.Background()
	log.Info().Str("dataset", datasetID).Str("fare_version", fareVersionID).Msg("Validating candidate fare version")
	for _, collection := range []struct {
		name string
		walk func() error
	}{
		{"fare_locations", func() error {
			return walkFareVersionRecords[ctdf.FareLocation](ctx, "fare_locations", filter, datasetID, fareVersionID, func(value ctdf.FareLocation) error { return value.Validate() })
		}},
		{"fare_tariffs", func() error {
			return walkFareVersionRecords[ctdf.FareTariff](ctx, "fare_tariffs", filter, datasetID, fareVersionID, func(value ctdf.FareTariff) error { return value.Validate() })
		}},
		{"fare_products", func() error {
			return walkFareVersionRecords[ctdf.FareProduct](ctx, "fare_products", filter, datasetID, fareVersionID, func(value ctdf.FareProduct) error { return value.Validate() })
		}},
		{"fare_rider_categories", func() error {
			return walkFareVersionRecords[ctdf.FareRiderCategory](ctx, "fare_rider_categories", filter, datasetID, fareVersionID, func(value ctdf.FareRiderCategory) error { return value.Validate() })
		}},
		{"fare_media", func() error {
			return walkFareVersionRecords[ctdf.FareMedia](ctx, "fare_media", filter, datasetID, fareVersionID, validateFareMedia)
		}},
		{"fare_networks", func() error {
			return walkFareVersionRecords[ctdf.FareNetwork](ctx, "fare_networks", filter, datasetID, fareVersionID, nil)
		}},
		{"fare_route_networks", func() error {
			return walkFareVersionRecords[ctdf.FareRouteNetwork](ctx, "fare_route_networks", filter, datasetID, fareVersionID, validateFareRouteNetwork)
		}},
		{"fare_timeframes", func() error {
			return walkFareVersionRecords[ctdf.FareTimeframe](ctx, "fare_timeframes", filter, datasetID, fareVersionID, validateFareTimeframe)
		}},
		{"fare_offers", func() error {
			return walkFareVersionRecords[ctdf.FareOffer](ctx, "fare_offers", filter, datasetID, fareVersionID, func(value ctdf.FareOffer) error { return value.Validate() })
		}},
		{"fare_leg_rules", func() error {
			return walkFareVersionRecords[ctdf.FareLegRule](ctx, "fare_leg_rules", filter, datasetID, fareVersionID, func(value ctdf.FareLegRule) error { return value.Validate() })
		}},
		{"fare_transfer_rules", func() error {
			return walkFareVersionRecords[ctdf.FareTransferRule](ctx, "fare_transfer_rules", filter, datasetID, fareVersionID, func(value ctdf.FareTransferRule) error { return value.Validate() })
		}},
		{"fare_effective_leg_joins", func() error {
			return walkFareVersionRecords[ctdf.FareEffectiveLegJoin](ctx, "fare_effective_leg_joins", filter, datasetID, fareVersionID, nil)
		}},
		{"fare_caps", func() error {
			return walkFareVersionRecords[ctdf.FareCap](ctx, "fare_caps", filter, datasetID, fareVersionID, validateFareCap)
		}},
	} {
		started := time.Now()
		log.Info().Str("dataset", datasetID).Str("collection", collection.name).Msg("Validating fare records")
		if err := collection.walk(); err != nil {
			return fmt.Errorf("validate %s: %w", collection.name, err)
		}
		log.Info().Str("dataset", datasetID).Str("collection", collection.name).Dur("elapsed", time.Since(started)).Msg("Validated fare records")
	}

	for _, ref := range []struct {
		collection string
		field      string
		array      bool
		target     string
		targetKey  string
	}{
		{"fare_locations", "parentref", false, "fare_locations", "primaryidentifier"},
		{"fare_products", "riderref", false, "fare_rider_categories", "primaryidentifier"},
		{"fare_products", "mediaref", false, "fare_media", "primaryidentifier"},
		{"fare_offers", "productref", false, "fare_products", "primaryidentifier"},
		{"fare_offers", "tariffref", false, "fare_tariffs", "primaryidentifier"},
		{"fare_leg_rules", "productref", false, "fare_products", "primaryidentifier"},
		{"fare_leg_rules", "productrefs", true, "fare_products", "primaryidentifier"},
		{"fare_leg_rules", "offerref", false, "fare_offers", "primaryidentifier"},
		{"fare_leg_rules", "offerrefs", true, "fare_offers", "primaryidentifier"},
		{"fare_leg_rules", "riderref", false, "fare_rider_categories", "primaryidentifier"},
		{"fare_leg_rules", "mediaref", false, "fare_media", "primaryidentifier"},
		{"fare_leg_rules", "originref", false, "fare_locations", "primaryidentifier"},
		{"fare_leg_rules", "destinationref", false, "fare_locations", "primaryidentifier"},
		{"fare_leg_rules", "viarefs", true, "fare_locations", "primaryidentifier"},
		{"fare_leg_rules", "networkref", false, "fare_networks", "primaryidentifier"},
		{"fare_leg_rules", "fromtimeframeref", false, "fare_timeframes", "groupref"},
		{"fare_leg_rules", "totimeframeref", false, "fare_timeframes", "groupref"},
		{"fare_route_networks", "networkref", false, "fare_networks", "primaryidentifier"},
		{"fare_effective_leg_joins", "fromnetworkref", false, "fare_networks", "primaryidentifier"},
		{"fare_effective_leg_joins", "tonetworkref", false, "fare_networks", "primaryidentifier"},
		{"fare_effective_leg_joins", "fromtimeframeref", false, "fare_timeframes", "groupref"},
		{"fare_effective_leg_joins", "totimeframeref", false, "fare_timeframes", "groupref"},
		{"fare_transfer_rules", "fromleggroupref", false, "fare_leg_rules", "leggroupref"},
		{"fare_transfer_rules", "toleggroupref", false, "fare_leg_rules", "leggroupref"},
		{"fare_transfer_rules", "productref", false, "fare_products", "primaryidentifier"},
		{"fare_transfer_rules", "productrefs", true, "fare_products", "primaryidentifier"},
		{"fare_caps", "eligibleproductrefs", true, "fare_products", "primaryidentifier"},
		{"fare_caps", "eligiblemediarefs", true, "fare_media", "primaryidentifier"},
		{"fare_caps", "eligibleriderrefs", true, "fare_rider_categories", "primaryidentifier"},
		{"fare_caps", "locationrefs", true, "fare_locations", "primaryidentifier"},
	} {
		started := time.Now()
		log.Info().
			Str("dataset", datasetID).
			Str("collection", ref.collection).
			Str("field", ref.field).
			Msg("Checking fare references")
		if err := checkFareReference(ctx, ref.collection, filter, ref.field, ref.array, ref.target, ref.targetKey); err != nil {
			return err
		}
		log.Info().
			Str("dataset", datasetID).
			Str("collection", ref.collection).
			Str("field", ref.field).
			Dur("elapsed", time.Since(started)).
			Msg("Checked fare references")
	}
	log.Info().Str("dataset", datasetID).Str("fare_version", fareVersionID).Msg("Validated candidate fare version")
	return nil
}

type fareVersionedRecord interface {
	GetFareDatasetID() string
	GetFareVersionID() string
	GetFareRecord() ctdf.FareRecord
}

func walkFareVersionRecords[T fareVersionedRecord](ctx context.Context, collectionName string, filter bson.M, datasetID, fareVersionID string, validate func(T) error) error {
	started := time.Now()
	cursor, err := database.GetCollection(collectionName).Find(ctx, filter, options.Find().SetBatchSize(256))
	if err != nil {
		return fmt.Errorf("read records: %w", err)
	}
	defer cursor.Close(ctx)
	var checked int64
	lastProgress := started
	for cursor.Next(ctx) {
		var value T
		if err := cursor.Decode(&value); err != nil {
			return fmt.Errorf("decode record: %w", err)
		}
		record := value.GetFareRecord()
		if value.GetFareDatasetID() != datasetID || value.GetFareVersionID() != fareVersionID {
			return fmt.Errorf("record %q belongs to another dataset or fare version", record.PrimaryIdentifier)
		}
		if err := record.Validate(); err != nil {
			return fmt.Errorf("record %q: %w", record.PrimaryIdentifier, err)
		}
		if validate != nil {
			if err := validate(value); err != nil {
				return fmt.Errorf("record %q: %w", record.PrimaryIdentifier, err)
			}
		}
		checked++
		if checked%250_000 == 0 || checked%10_000 == 0 && time.Since(lastProgress) >= 30*time.Second {
			log.Info().
				Str("dataset", datasetID).
				Str("collection", collectionName).
				Int64("records", checked).
				Dur("elapsed", time.Since(started)).
				Msg("Fare record validation progress")
			lastProgress = time.Now()
		}
	}
	if err := cursor.Err(); err != nil {
		return fmt.Errorf("iterate records: %w", err)
	}
	return nil
}

func validateFareMedia(value ctdf.FareMedia) error {
	switch value.Type {
	case ctdf.FareMediaTypeNone, ctdf.FareMediaTypePaper, ctdf.FareMediaTypeTransitCard, ctdf.FareMediaTypeApp, ctdf.FareMediaTypeContactless, ctdf.FareMediaTypeOther:
		return nil
	default:
		return fmt.Errorf("unsupported fare media type %q", value.Type)
	}
}

func validateFareRouteNetwork(value ctdf.FareRouteNetwork) error {
	if value.RouteRef == "" || value.NetworkRef == "" {
		return errors.New("fare route network requires route and network references")
	}
	return nil
}

func validateFareTimeframe(value ctdf.FareTimeframe) error {
	if value.GroupRef == "" || value.StartTimeSecond < 0 || value.EndTimeSecond <= value.StartTimeSecond {
		return errors.New("fare timeframe has an invalid group or time range")
	}
	return nil
}

func validateFareCap(value ctdf.FareCap) error {
	return value.Price.ValidateNonNegative()
}

func checkFareReference(ctx context.Context, collectionName string, filter bson.M, field string, array bool, targetCollection, targetKey string) error {
	const foundField = "__fare_reference_matches"
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: filter}},
	}
	if array {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{
			"$expr": bson.M{"$gt": bson.A{
				bson.M{"$size": bson.M{"$ifNull": bson.A{"$" + field, bson.A{}}}},
				0,
			}},
		}}})
	} else {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{
			field: bson.M{"$exists": true, "$nin": bson.A{"", nil}},
		}}})
	}
	lookupPipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.M{"datasetid": filter["datasetid"], "fareversionid": filter["fareversionid"]}}},
	}
	if !array {
		lookupPipeline = append(lookupPipeline, bson.D{{Key: "$limit", Value: 1}})
	}
	pipeline = append(pipeline, bson.D{{Key: "$lookup", Value: bson.D{
		{Key: "from", Value: targetCollection},
		{Key: "localField", Value: field},
		{Key: "foreignField", Value: targetKey},
		{Key: "pipeline", Value: lookupPipeline},
		{Key: "as", Value: foundField},
	}}})
	if array {
		pipeline = append(pipeline, bson.D{{Key: "$set", Value: bson.M{
			"__fare_missing_references": bson.M{"$setDifference": bson.A{
				bson.M{"$ifNull": bson.A{"$" + field, bson.A{}}},
				bson.M{"$map": bson.M{"input": "$" + foundField, "as": "fare_reference", "in": "$$fare_reference." + targetKey}},
			}},
		}}})
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{
			"$expr": bson.M{"$gt": bson.A{bson.M{"$size": "$__fare_missing_references"}, 0}},
		}}})
	} else {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{
			foundField + ".0": bson.M{"$exists": false},
		}}})
	}
	pipeline = append(pipeline,
		bson.D{{Key: "$project", Value: bson.M{"_id": 0, "primaryidentifier": 1, field: 1, "__fare_missing_references": 1}}},
		bson.D{{Key: "$limit", Value: 1}},
	)
	cursor, err := database.GetCollection(collectionName).Aggregate(ctx, pipeline, options.Aggregate().SetAllowDiskUse(true))
	if err != nil {
		return fmt.Errorf("check %s.%s references: %w", collectionName, field, err)
	}
	defer cursor.Close(ctx)
	if cursor.Next(ctx) {
		var invalid bson.M
		if err := cursor.Decode(&invalid); err != nil {
			return fmt.Errorf("decode dangling %s.%s reference: %w", collectionName, field, err)
		}
		return fmt.Errorf("%s %q references missing fare record through %s: %v", collectionName, invalid["primaryidentifier"], field, invalid["__fare_missing_references"])
	}
	if err := cursor.Err(); err != nil {
		return fmt.Errorf("iterate %s.%s references: %w", collectionName, field, err)
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
	if !supported.Fares {
		report.FareDataPresent = false
		report.ImportedFareLocations = 0
		report.FareLocationsLinkedToStops = 0
		report.FareLocationsWithoutStopLink = 0
		report.ImportedFareProducts = 0
		report.ImportedFareOffers = 0
		report.ImportedFareRules = 0
		report.ImportedFareMedia = 0
		report.ImportedFareRiderCategories = 0
		report.ImportedFareTariffs = 0
		report.ImportedFareNetworks = 0
		report.ImportedFareRouteNetworks = 0
		report.ImportedFareTimeframes = 0
		report.ImportedFareLegJoins = 0
		report.ImportedFareTransferRules = 0
		report.UncoveredFareValidityIntervals = 0
		report.FareValidityGapDetails = nil
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
