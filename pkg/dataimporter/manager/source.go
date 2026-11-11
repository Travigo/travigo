package manager

import (
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
	"github.com/travigo/travigo/pkg/dataimporter/formats/gtfs"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func hasPublisherGTFSVersion(dataset *datasets.DataSet) bool {
	return dataset != nil && dataset.Format == datasets.DataSetFormatGTFSSchedule &&
		(dataset.UnpackBundle == "" || dataset.UnpackBundle == datasets.BundleFormatNone)
}

// CanPrepareDatasetSource reports whether smart-mode preflight can stage this source.
func CanPrepareDatasetSource(dataset *datasets.DataSet) bool {
	if dataset == nil || dataset.ImportDestination == datasets.ImportDestinationRealtimeQueue ||
		dataset.ImportDestination == datasets.ImportDestinationSpecificRunner || dataset.Format == datasets.DataSetFormatGTFSRealtime ||
		dataset.Format == datasets.DataSetFormatSiriVM || dataset.Format == datasets.DataSetFormatSiriSX || !isValidUrl(dataset.Source) ||
		(dataset.SourceAuthentication != nil && dataset.SourceAuthentication.Custom != "" && dataset.SourceAuthentication.Custom != "gb-nationalrail-login") {
		return false
	}
	format, err := createDatasetFormat(dataset)
	if err != nil {
		return false
	}
	_, isAPI := format.(formats.APIFormat)
	return !isAPI
}

// PrepareDatasetSource conditionally fetches a remote static source into destination. It never
// records a version: the normal importer remains responsible for publishing a successful import.
func PrepareDatasetSource(ctx context.Context, dataset *datasets.DataSet, forceImport bool, destination *os.File) (bool, string, error) {
	if !CanPrepareDatasetSource(dataset) || destination == nil {
		return false, "", errors.New("dataset source cannot be prepared")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var version ctdf.DatasetVersion
	err := database.GetCollection("dataset_versions").FindOne(ctx, bson.M{"dataset": dataset.Identifier}).Decode(&version)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return false, "", fmt.Errorf("load dataset version: %w", err)
	}
	versionFound := err == nil
	if versionFound && shouldSkipFreshDataset(dataset, &version, forceImport, time.Now()) {
		return false, version.ETag, nil
	}
	var etag string
	if versionFound && !forceImport {
		etag = version.ETag
	}
	req, err := publisherRequest(ctx, dataset, etag)
	if err != nil {
		return false, "", err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return false, "", safeHTTPError("download file", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		if etag == "" {
			return false, "", errors.New("unexpected HTTP 304 without conditional request")
		}
		return false, etag, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("download file: unexpected HTTP status %s", resp.Status)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(destination, h), resp.Body)
	if err != nil {
		return false, "", fmt.Errorf("save downloaded file: %w", err)
	}
	newETag := resp.Header.Get("ETag")
	if versionFound && version.Hash == hex.EncodeToString(h.Sum(nil)) && !forceImport {
		return false, newETag, nil
	}
	if hasPublisherGTFSVersion(dataset) {
		provided := gtfs.ReadScheduleFeedVersion(destination, n)
		if !forceImport && versionFound && provided != "" && provided == version.DatasetProvidedVersion {
			return false, newETag, nil
		}
	}
	return true, newETag, nil
}

func safeHTTPError(operation string, err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s: %w", operation, urlErr.Err)
	}
	return fmt.Errorf("%s failed", operation)
}

func publisherRequest(ctx context.Context, dataset *datasets.DataSet, etag string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dataset.Source, nil)
	if err != nil {
		return nil, safeHTTPError("create publisher request", err)
	}
	// Some providers, including TfL, reject requests without this user agent.
	req.Header.Set("User-Agent", "curl/7.54.1")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if err := applySourceAuthentication(req, dataset); err != nil {
		return nil, err
	}
	return req, nil
}

func applySourceAuthentication(req *http.Request, dataset *datasets.DataSet) error {
	if dataset.SourceAuthentication == nil {
		return nil
	}
	env := util.GetEnvironmentVariables()
	for key, variable := range dataset.SourceAuthentication.Query {
		if env[variable] == "" {
			return fmt.Errorf("%s must be set", variable)
		}
		q := req.URL.Query()
		q.Add(key, env[variable])
		req.URL.RawQuery = q.Encode()
	}
	auth := dataset.SourceAuthentication
	if auth.Basic.Username != "" && auth.Basic.Password != "" {
		if env[auth.Basic.Username] == "" {
			return fmt.Errorf("%s must be set", auth.Basic.Username)
		}
		if env[auth.Basic.Password] == "" {
			return fmt.Errorf("%s must be set", auth.Basic.Password)
		}
		req.SetBasicAuth(env[auth.Basic.Username], env[auth.Basic.Password])
	}
	for key, variable := range auth.Header {
		if env[variable] == "" {
			return fmt.Errorf("%s must be set", variable)
		}
		req.Header.Set(key, env[variable])
	}
	if auth.AuthHeader != "" {
		if env[auth.AuthHeader] == "" {
			return fmt.Errorf("%s must be set", auth.AuthHeader)
		}
		req.Header.Set("Authorization", "Bearer "+env[auth.AuthHeader])
	}
	if auth.Custom == "gb-nationalrail-login" {
		token, err := customAuthNationalRailLogin(req.Context(), http.DefaultClient, "https://opendata.nationalrail.co.uk/authenticate",
			env["TRAVIGO_NATIONALRAIL_USERNAME"], env["TRAVIGO_NATIONALRAIL_PASSWORD"])
		if err != nil {
			return err
		}
		req.Header.Set("X-Auth-Token", token)
	}
	return nil
}

// ImportDatasetWithSourceOverride imports a staged artifact fetched by the internal runner.
// The source URL is fetched without publisher credentials and without following redirects.
func ImportDatasetWithSourceOverride(ctx context.Context, dataset *datasets.DataSet, forceImport bool, skipCleanup bool, sourceURL string) error {
	if !CanPrepareDatasetSource(dataset) || !isValidUrl(sourceURL) {
		return errors.New("invalid dataset or URL for staged source override")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return safeHTTPError("create internal source request", err)
	}
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	file, err := os.CreateTemp(os.TempDir(), "travigo-staged-import-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	etag, fetchErr := fetchStagedSource(client, req, file, 2*time.Second)
	closeErr := file.Close()
	if fetchErr != nil {
		return fetchErr
	}
	if closeErr != nil {
		return closeErr
	}
	return importDataset(dataset, forceImport, skipCleanup, name, etag)
}

// The runner can be temporarily unavailable while its single pod is replaced.
func fetchStagedSource(client *http.Client, req *http.Request, file *os.File, delay time.Duration) (string, error) {
	for {
		resp, err := client.Do(req)
		if err != nil {
			err = safeHTTPError("fetch internal source override", err)
		} else if resp.StatusCode == http.StatusOK {
			_, err = io.Copy(file, resp.Body)
			resp.Body.Close()
			if err == nil {
				return resp.Header.Get("ETag"), nil
			}
			var diskErr *os.PathError
			if errors.As(err, &diskErr) {
				return "", fmt.Errorf("save internal source override: %w", err)
			}
			if resetErr := file.Truncate(0); resetErr != nil {
				return "", resetErr
			}
			if _, resetErr := file.Seek(0, io.SeekStart); resetErr != nil {
				return "", resetErr
			}
		} else {
			resp.Body.Close()
			err = fmt.Errorf("fetch internal source override: unexpected HTTP status %s", resp.Status)
			if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				return "", err
			}
		}

		timer := time.NewTimer(delay)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return "", fmt.Errorf("fetch internal source override: %w (last attempt: %v)", req.Context().Err(), err)
		case <-timer.C:
		}
		delay = min(2*delay, 15*time.Second)
	}
}

func tempDownloadFile(dataset *datasets.DataSet, etag string) (bool, *os.File, string, error) {
	req, err := publisherRequest(context.Background(), dataset, etag)
	if err != nil {
		return false, nil, "", err
	}

	// Performance Review [Set D] [PR067] [P2] (batch): There is no download deadline, cancellation
	// or byte limit. A slow body can pin a scheduled SIRI/GTFS worker indefinitely
	// and grow its temporary file. Use a shared configured client with dataset-aware
	// transfer budgets, idle/progress limits and capped retry/backoff; retain the
	// existing status/ETag validation and measure download time separately from import.
	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		return false, nil, "", safeHTTPError("download file", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return false, nil, "", nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return false, nil, "", fmt.Errorf("download file: unexpected HTTP status %s", resp.Status)
	}

	tmpFile, err := os.CreateTemp(os.TempDir(), "travigo-data-importer-")
	if err != nil {
		return false, nil, "", fmt.Errorf("create temporary file: %w", err)
	}

	log.Debug().Str("path", tmpFile.Name()).Msg("Data file downloaded")

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return false, nil, "", fmt.Errorf("save downloaded file: %w", err)
	}

	// The caller owns the open file and its temporary path.
	return true, tmpFile, resp.Header.Get("Etag"), nil
}
