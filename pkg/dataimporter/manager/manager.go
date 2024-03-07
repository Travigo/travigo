package manager

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/formats"
	"github.com/travigo/travigo/pkg/dataimporter/formats/travelinenoc"
)

func GetDataset(identifier string) (DataSet, error) {
	registered := GetRegisteredDataSets()

	for _, dataset := range registered {
		if dataset.Identifier == identifier {
			return dataset, nil
		}
	}

	return DataSet{}, errors.New("Dataset could not be found")
}

func ImportDataset(identifier string) error {
	dataset, err := GetDataset(identifier)

	if err != nil {
		return err
	}

	log.Info().Interface("dataset", dataset).Msg("Found dataset")

	if dataset.UnpackBundle != BundleFormatNone {
		return errors.New("Cannot handle bundled type yet")
	}

	var format formats.Format

	switch dataset.Format {
	case DataSetFormatTravelineNOC:
		format = &travelinenoc.TravelineData{}
	default:
		return errors.New(fmt.Sprintf("Unrecognised format %s", dataset.Format))
	}

	source := dataset.Source
	if isValidUrl(dataset.Source) {
		var tempFile *os.File
		tempFile, _ = tempDownloadFile(dataset.Source)

		source = tempFile.Name()
		defer os.Remove(tempFile.Name())
	}

	file, err := os.Open(source)
	if err != nil {
		return err
	}

	err = format.ParseFile(file)
	if err != nil {
		return err
	}

	err = format.ImportIntoMongoAsCTDF(&ctdf.DataSource{
		OriginalFormat: string(dataset.Format),
		Provider:       dataset.Provider.Name,
		DatasetID:      dataset.Identifier,
		Timestamp:      fmt.Sprintf("%d", time.Now().Unix()),
	})
	if err != nil {
		return err
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

func tempDownloadFile(source string, headers ...[]string) (*os.File, string) {
	req, _ := http.NewRequest("GET", source, nil)
	req.Header["user-agent"] = []string{"curl/7.54.1"} // TfL is protected by cloudflare and it gets angry when no user agent is set

	for _, header := range headers {
		req.Header[header[0]] = []string{header[1]}
	}

	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		log.Fatal().Err(err).Msg("Download file")
	}
	defer resp.Body.Close()

	_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	fileExtension := filepath.Ext(source)
	if err == nil {
		fileExtension = filepath.Ext(params["filename"])
	}

	tmpFile, err := os.CreateTemp(os.TempDir(), "travigo-data-importer-")
	if err != nil {
		log.Fatal().Err(err).Msg("Cannot create temporary file")
	}

	io.Copy(tmpFile, resp.Body)

	return tmpFile, fileExtension
}
