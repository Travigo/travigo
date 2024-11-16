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
	"net/http"
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
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
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
	case datasets.DataSetFormatGTFSRealtime:
		format = &gtfs.Realtime{}
	case datasets.DataSetFormatCIF:
		format = &cif.CommonInterfaceFormat{}
	case datasets.DataSetFormatTransXChange:
		format = &transxchange.TransXChange{}
	default:
		return nil, errors.New(fmt.Sprintf("Unrecognised format %s", dataset.Format))
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

func ImportDataset(dataset *datasets.DataSet, forceImport bool) error {
	datasetVersionCollection := database.GetCollection("dataset_versions")

	var existingDatasetVersion *ctdf.DatasetVersion
	datasetVersionCollection.FindOne(context.Background(), bson.M{"dataset": dataset.Identifier}).Decode(&existingDatasetVersion)

	var existingEtag string
	if existingDatasetVersion != nil && !forceImport {
		log.Info().Interface("version", existingDatasetVersion).Msg("Existing dataset version found")
		existingEtag = existingDatasetVersion.ETag
	}

	datasource := &ctdf.DataSourceReference{
		OriginalFormat: string(dataset.Format),
		ProviderName:   dataset.Provider.Name,
		ProviderID:     dataset.DataSourceRef,
		DatasetID:      dataset.Identifier,
		Timestamp:      fmt.Sprintf("%d", time.Now().Unix()),
	}

	source := dataset.Source
	var etag string

	if isValidUrl(dataset.Source) {
		var tempFile *os.File
		var hasChanged bool
		hasChanged, tempFile, etag = tempDownloadFile(dataset, existingEtag)

		if !hasChanged {
			log.Info().Str("dataset", dataset.Identifier).Msg("File ETag is not new, skipping processing")
			return nil
		}

		source = tempFile.Name()
		defer os.Remove(tempFile.Name())
	}

	// Calculate the hash of the file
	f, err := os.Open(source)
	defer f.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		log.Error().Err(err).Msg("Calculating hash")
	}
	sourceFileHash := hex.EncodeToString(hash.Sum(nil))

	// Check if the file hasn't changed
	if existingDatasetVersion != nil && existingDatasetVersion.Hash == sourceFileHash && !forceImport {
		log.Info().Str("dataset", dataset.Identifier).Msg("File hash is not new, skipping processing")
		return nil
	}

	// Parse the file
	sourceFileReaders := []io.Reader{}

	file, err := os.Open(source)
	if err != nil {
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
			panic(err)
		}
		defer archive.Close()

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
		err = format.Import(*dataset, datasource)
		if err != nil {
			return err
		}
	}

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
		cleanupOldRecords("services", datasource)
	}
	if dataset.SupportedObjects.Journeys {
		cleanupOldRecords("journeys", datasource)
	}

	// Update dataset version
	if dataset.ImportDestination != datasets.ImportDestinationRealtimeQueue {
		datasetVersion := ctdf.DatasetVersion{
			Dataset:      dataset.Identifier,
			Hash:         sourceFileHash,
			ETag:         etag,
			LastModified: time.Now(),
		}

		opts := options.Update().SetUpsert(true)
		_, err = datasetVersionCollection.UpdateOne(context.Background(), bson.M{"dataset": datasetVersion.Dataset}, bson.M{"$set": datasetVersion}, opts)
	}

	return nil
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

func tempDownloadFile(dataset *datasets.DataSet, etag string) (bool, *os.File, string) {
	req, _ := http.NewRequest("GET", dataset.Source, nil)
	req.Header.Set("user-agent", "curl/7.54.1") // TfL is protected by cloudflare and it gets angry when no user agent is set

	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	//// Handle authentication ////
	env := util.GetEnvironmentVariables()
	// Query paramaters
	for queryKey, queryValue := range dataset.SourceAuthentication.Query {
		if env[queryValue] == "" {
			log.Fatal().Msgf("%s must be set", queryValue)
		}

		q := req.URL.Query()
		q.Add(queryKey, env[queryValue])
		req.URL.RawQuery = q.Encode()
	}
	// Basic auth
	if dataset.SourceAuthentication.Basic.Username != "" && dataset.SourceAuthentication.Basic.Password != "" {
		if env[dataset.SourceAuthentication.Basic.Username] == "" {
			log.Fatal().Msgf("%s must be set", dataset.SourceAuthentication.Basic.Username)
		}
		if env[dataset.SourceAuthentication.Basic.Password] == "" {
			log.Fatal().Msgf("%s must be set", dataset.SourceAuthentication.Basic.Password)
		}

		req.SetBasicAuth(env[dataset.SourceAuthentication.Basic.Username], env[dataset.SourceAuthentication.Basic.Password])
	}
	// Headers
	for headerKey, headerValue := range dataset.SourceAuthentication.Header {
		if env[headerValue] == "" {
			log.Fatal().Msgf("%s must be set", headerValue)
		}

		req.Header.Set(headerKey, env[headerValue])
	}
	// Customs
	switch dataset.SourceAuthentication.Custom {
	case "gb-nationalrail-login":
		token := customAuthNationalRailLogin()
		req.Header.Set("X-Auth-Token", token)
	}

	// TODO delete me later
	if dataset.DownloadHandler != nil {
		dataset.DownloadHandler(req)
	}

	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		log.Fatal().Err(err).Msg("Download file")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return false, nil, ""
	}

	tmpFile, err := os.CreateTemp(os.TempDir(), "travigo-data-importer-")
	if err != nil {
		log.Fatal().Err(err).Msg("Cannot create temporary file")
	}

	log.Debug().Str("path", tmpFile.Name()).Msg("Data file downloaded")

	io.Copy(tmpFile, resp.Body)

	return true, tmpFile, resp.Header.Get("Etag")
}

func cleanupOldRecords(collectionName string, datasource *ctdf.DataSourceReference) {
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
