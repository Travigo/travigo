package netex

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
)

const (
	bodsFaresAPIHost     = "data.bus-data.dft.gov.uk"
	maximumFaresPages    = 100
	maximumFaresMetadata = 8 << 20
	maximumFaresDownload = 128 << 20
	maximumFaresXML      = 256 << 20
)

type faresMetadataPage struct {
	Next    string               `json:"next"`
	Results []faresMetadataEntry `json:"results"`
}

type faresMetadataEntry struct {
	ID               json.RawMessage `json:"id"`
	URL              string          `json:"url"`
	DownloadURL      string          `json:"download_url"`
	DownloadURLCamel string          `json:"downloadUrl"`
	DataURL          string          `json:"data_url"`
	NOC              stringList      `json:"noc"`
	OperatorNOC      stringList      `json:"operator_noc"`
	NationalOperator stringList      `json:"national_operator_code"`
	NOCs             stringList      `json:"nocs"`
	OperatorNOCs     stringList      `json:"operator_nocs"`
}

type stringList []string

func (values *stringList) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		if single = strings.TrimSpace(single); single != "" {
			*values = append(*values, single)
		}
		return nil
	}
	var multiple []string
	if err := json.Unmarshal(data, &multiple); err != nil {
		return err
	}
	for _, value := range multiple {
		if value = strings.TrimSpace(value); value != "" {
			*values = append(*values, value)
		}
	}
	return nil
}

// ImportAPI discovers currently published BODS fare feeds and imports their
// NeTEx documents into one candidate catalogue version.
func (format *Fares) ImportAPI(dataset datasets.DataSet, datasource *ctdf.DataSourceReference) (datasets.DataImportReport, error) {
	if datasource == nil || datasource.FareVersionID == "" {
		return datasets.DataImportReport{}, fmt.Errorf("BODS fare import requires a candidate fare version")
	}
	importStarted := time.Now()
	apiKey, err := bodsAPIKey(dataset)
	if err != nil {
		log.Error().Err(err).Str("dataset", dataset.Identifier).Str("stage", "authentication").Dur("stage_elapsed", time.Since(importStarted)).Msg("BODS fare import failed")
		return datasets.DataImportReport{}, err
	}
	client := &http.Client{
		Timeout: 90 * time.Second,
		CheckRedirect: func(request *http.Request, previous []*http.Request) error {
			if len(previous) >= 5 || request.URL.Scheme != "https" || !strings.EqualFold(request.URL.Hostname(), bodsFaresAPIHost) {
				return fmt.Errorf("refusing redirect outside the BODS HTTPS host")
			}
			query := request.URL.Query()
			if query.Get("api_key") == "" {
				query.Set("api_key", apiKey)
				request.URL.RawQuery = query.Encode()
			}
			return nil
		},
	}
	ctx, cancel := context.WithCancel(datasource.GetImportContext())
	defer cancel()
	importDatasource := *datasource
	importDatasource.ImportContext = ctx

	metadataStarted := time.Now()
	log.Info().Str("dataset", dataset.Identifier).Msg("Loading published BODS fare dataset metadata")
	metadata, err := format.loadFaresMetadata(ctx, client, dataset.Source, apiKey)
	if err != nil {
		log.Error().Err(err).Str("dataset", dataset.Identifier).Str("stage", "metadata").Dur("stage_elapsed", time.Since(metadataStarted)).Msg("BODS fare import failed")
		return datasets.DataImportReport{}, err
	}
	log.Info().Str("dataset", dataset.Identifier).Int("published_fare_datasets", len(metadata)).Dur("stage_elapsed", time.Since(metadataStarted)).Msg("Loaded BODS fare dataset metadata")
	if len(metadata) == 0 {
		err := fmt.Errorf("BODS fares API returned no published fare datasets")
		log.Error().Err(err).Str("dataset", dataset.Identifier).Str("stage", "metadata").Dur("stage_elapsed", time.Since(metadataStarted)).Msg("BODS fare import failed")
		return datasets.DataImportReport{}, err
	}

	report := datasets.DataImportReport{}
	failedDatasetIDs := make([]string, 0)
	var firstDatasetError error
	recordDatasetFailure := func(entryID string, err error) {
		failedDatasetIDs = append(failedDatasetIDs, entryID)
		if firstDatasetError == nil {
			firstDatasetError = err
		}
	}
	for index, entry := range metadata {
		if err := ctx.Err(); err != nil {
			return report, fmt.Errorf("BODS fare import cancelled after %d datasets: %w", index, err)
		}
		entryStarted := time.Now()
		entryID := entry.identifier()
		entryLog := log.With().
			Str("dataset", dataset.Identifier).
			Str("fare_dataset_id", entryID).
			Int("fare_dataset_index", index+1).
			Int("fare_dataset_count", len(metadata)).
			Logger()
		if entry.identifier() == "unknown" {
			err := fmt.Errorf("BODS fare dataset metadata has no dataset id")
			entryLog.Error().Err(err).Str("stage", "metadata").Dur("elapsed", time.Since(entryStarted)).Msg("BODS fare dataset failed")
			recordDatasetFailure(entryID, err)
			continue
		}
		downloadURL := entry.downloadURL()
		if downloadURL == "" {
			err := fmt.Errorf("BODS fare dataset %s has no downloadable NeTEx URL", entryID)
			entryLog.Error().Err(err).Str("stage", "metadata").Dur("elapsed", time.Since(entryStarted)).Msg("BODS fare dataset failed")
			recordDatasetFailure(entryID, err)
			continue
		}
		downloadURL, err = resolveBODSFaresURL(dataset.Source, downloadURL)
		if err != nil {
			err = fmt.Errorf("BODS fare dataset %s: %w", entryID, err)
			entryLog.Error().Err(err).Str("stage", "metadata").Dur("elapsed", time.Since(entryStarted)).Msg("BODS fare dataset failed")
			recordDatasetFailure(entryID, err)
			continue
		}
		downloadStarted := time.Now()
		entryLog.Info().Msg("Downloading BODS fare dataset")
		body, err := fetchBODSFares(ctx, client, downloadURL, apiKey, maximumFaresDownload)
		if err != nil {
			err = fmt.Errorf("download BODS fare dataset %s: %w", entryID, err)
			entryLog.Error().Err(err).Str("stage", "download").Dur("stage_elapsed", time.Since(downloadStarted)).Msg("BODS fare dataset failed")
			recordDatasetFailure(entryID, err)
			continue
		}
		entryLog.Info().Int("download_bytes", len(body)).Dur("stage_elapsed", time.Since(downloadStarted)).Msg("Downloaded BODS fare dataset")
		feed := &Fares{SourceNamespace: entry.namespace(), SourceOperators: entry.operatorRefs()}
		parseStarted := time.Now()
		entryLog.Info().Msg("Parsing BODS fare dataset")
		if err := feed.parseNeTExPayload(body); err != nil {
			err = fmt.Errorf("parse BODS fare dataset %s: %w", entryID, err)
			entryLog.Error().Err(err).Str("stage", "parse").Int("download_bytes", len(body)).Dur("stage_elapsed", time.Since(parseStarted)).Msg("BODS fare dataset failed")
			recordDatasetFailure(entryID, err)
			continue
		}
		entryLog.Info().Int("fare_frames", len(findNodes(feed.root, "FareFrame"))).Dur("stage_elapsed", time.Since(parseStarted)).Msg("Parsed BODS fare dataset")
		feedImportStarted := time.Now()
		entryLog.Info().Msg("Importing parsed BODS fare dataset")
		feedReport, err := feed.Import(dataset, &importDatasource)
		if err != nil {
			err = fmt.Errorf("import BODS fare dataset %s: %w", entryID, err)
			if cleanupErr := discardFailedBODSFareDataset(ctx, dataset.Identifier, datasource.FareVersionID, entry.namespace()); cleanupErr != nil {
				cleanupErr = fmt.Errorf("discard partial records for failed BODS fare dataset %s: %w", entryID, cleanupErr)
				entryLog.Error().Err(cleanupErr).Str("stage", "cleanup").Dur("stage_elapsed", time.Since(feedImportStarted)).Msg("BODS fare dataset cleanup failed")
				return report, errors.Join(err, cleanupErr)
			}
			entryLog.Error().Err(err).Str("stage", "import").Int("download_bytes", len(body)).Int("fare_frames", len(findNodes(feed.root, "FareFrame"))).Dur("stage_elapsed", time.Since(feedImportStarted)).Msg("BODS fare dataset failed")
			recordDatasetFailure(entryID, err)
			continue
		}
		entryLog.Info().
			Int("fare_products", feedReport.ImportedFareProducts).
			Int("fare_offers", feedReport.ImportedFareOffers).
			Int("fare_rules", feedReport.ImportedFareRules).
			Int("fare_locations", feedReport.ImportedFareLocations).
			Dur("stage_elapsed", time.Since(feedImportStarted)).
			Msg("Imported BODS fare dataset")
		report.FareDataPresent = report.FareDataPresent || feedReport.FareDataPresent
		report.ImportedFareLocations += feedReport.ImportedFareLocations
		report.ImportedFareProducts += feedReport.ImportedFareProducts
		report.ImportedFareOffers += feedReport.ImportedFareOffers
		report.ImportedFareRules += feedReport.ImportedFareRules
		report.ImportedFareMedia += feedReport.ImportedFareMedia
		report.ImportedFareRiderCategories += feedReport.ImportedFareRiderCategories
		report.ImportedFareTariffs += feedReport.ImportedFareTariffs
		report.ImportedFareNetworks += feedReport.ImportedFareNetworks
		report.ImportedFareRouteNetworks += feedReport.ImportedFareRouteNetworks
		report.ImportedFareTimeframes += feedReport.ImportedFareTimeframes
		report.ImportedFareLegJoins += feedReport.ImportedFareLegJoins
		report.ImportedFareTransferRules += feedReport.ImportedFareTransferRules
		report.MergeFareValidityGaps(feedReport)
	}
	if err := ctx.Err(); err != nil {
		return report, fmt.Errorf("BODS fare import cancelled: %w", err)
	}
	failedDatasetIDsForLog := failedDatasetIDs
	if len(failedDatasetIDsForLog) > 5 {
		failedDatasetIDsForLog = append(append([]string(nil), failedDatasetIDsForLog[:5]...), "...")
	}
	completionLog := log.With().
		Str("dataset", dataset.Identifier).
		Int("fare_dataset_count", len(metadata)).
		Int("failed_fare_datasets", len(failedDatasetIDs)).
		Strs("failed_fare_dataset_ids", failedDatasetIDsForLog).
		Int("fare_products", report.ImportedFareProducts).
		Int("fare_offers", report.ImportedFareOffers).
		Int("fare_rules", report.ImportedFareRules).
		Dur("elapsed", time.Since(importStarted)).
		Logger()
	if len(failedDatasetIDs) > 0 {
		completionLog.Warn().Msg("Completed BODS fare API import with failed datasets")
	} else {
		completionLog.Info().Msg("Completed BODS fare API import")
	}
	if len(failedDatasetIDs) == len(metadata) {
		return report, fmt.Errorf("all %d published BODS fare datasets failed (sample ids=%v): %w", len(metadata), failedDatasetIDsForLog, firstDatasetError)
	}
	return report, nil
}

func bodsFareFeedRecordFilter(datasetID, fareVersionID, namespace string) bson.M {
	return bson.M{
		"datasetid":        datasetID,
		"fareversionid":    fareVersionID,
		"sourceidentifier": bson.M{"$regex": "^" + regexp.QuoteMeta(namespace) + ":"},
	}
}

func discardFailedBODSFareDataset(ctx context.Context, datasetID, fareVersionID, namespace string) error {
	filter := bodsFareFeedRecordFilter(datasetID, fareVersionID, namespace)
	for _, collection := range []string{
		"fare_locations",
		"fare_rider_categories",
		"fare_media",
		"fare_products",
		"fare_tariffs",
		"fare_offers",
		"fare_leg_rules",
	} {
		if _, err := database.GetCollection(collection).DeleteMany(ctx, filter); err != nil {
			return fmt.Errorf("delete partial records from %s: %w", collection, err)
		}
	}
	return nil
}

func (format *Fares) loadFaresMetadata(ctx context.Context, client *http.Client, source, apiKey string) ([]faresMetadataEntry, error) {
	pageURL := source
	seenPages := map[string]bool{}
	entries := make([]faresMetadataEntry, 0)
	for page := 0; page < maximumFaresPages && pageURL != ""; page++ {
		requestURL, err := bodsAuthenticatedURL(pageURL, apiKey)
		if err != nil {
			return nil, fmt.Errorf("page %d: invalid BODS fares API URL: %w", page+1, err)
		}
		if seenPages[requestURL] {
			return nil, fmt.Errorf("page %d: BODS fares API pagination repeated a page", page+1)
		}
		seenPages[requestURL] = true
		body, err := fetchBODSFares(ctx, client, requestURL, "", maximumFaresMetadata)
		if err != nil {
			return nil, fmt.Errorf("page %d: request BODS fares metadata: %w", page+1, err)
		}
		var response faresMetadataPage
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, fmt.Errorf("page %d: decode BODS fares metadata: %w", page+1, err)
		}
		if len(response.Results) == 0 && page == 0 {
			return nil, fmt.Errorf("page %d: BODS fares API response has no results array", page+1)
		}
		entries = append(entries, response.Results...)
		pageURL = strings.TrimSpace(response.Next)
		if pageURL != "" {
			pageURL, err = resolveBODSFaresURL(requestURL, pageURL)
			if err != nil {
				return nil, fmt.Errorf("page %d: invalid BODS fares pagination URL: %w", page+1, err)
			}
		}
	}
	if pageURL != "" {
		return nil, fmt.Errorf("BODS fares API exceeded %d pages", maximumFaresPages)
	}
	return entries, nil
}

func bodsAPIKey(dataset datasets.DataSet) (string, error) {
	if dataset.SourceAuthentication == nil {
		return "", fmt.Errorf("BODS fares API authentication is not configured")
	}
	environment := util.GetEnvironmentVariables()
	for key, variable := range dataset.SourceAuthentication.Query {
		if key != "api_key" {
			continue
		}
		if value := strings.TrimSpace(environment[variable]); value != "" {
			return value, nil
		}
		return "", fmt.Errorf("%s must be set", variable)
	}
	return "", fmt.Errorf("BODS fares API key is not configured")
}

func bodsAuthenticatedURL(value, apiKey string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), bodsFaresAPIHost) {
		return "", fmt.Errorf("BODS fares links must use HTTPS on %s", bodsFaresAPIHost)
	}
	query := parsed.Query()
	if query.Get("api_key") == "" {
		query.Set("api_key", apiKey)
		parsed.RawQuery = query.Encode()
	}
	return parsed.String(), nil
}

func resolveBODSFaresURL(baseURL, value string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	link, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	resolved := base.ResolveReference(link)
	if resolved.Scheme != "https" || !strings.EqualFold(resolved.Hostname(), bodsFaresAPIHost) {
		return "", fmt.Errorf("refusing fare URL outside the BODS HTTPS host")
	}
	return resolved.String(), nil
}

func fetchBODSFares(ctx context.Context, client *http.Client, source, apiKey string, maximumBytes int64) ([]byte, error) {
	parsed, err := url.Parse(source)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), bodsFaresAPIHost) {
		return nil, fmt.Errorf("refusing non-BODS or non-HTTPS download URL")
	}
	if apiKey != "" {
		query := parsed.Query()
		if query.Get("api_key") == "" {
			query.Set("api_key", apiKey)
			parsed.RawQuery = query.Encode()
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Travigo fare importer")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s", safeBODSFaresRequestError(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if int64(len(body)) > maximumBytes {
		return nil, fmt.Errorf("response exceeded %d bytes", maximumBytes)
	}
	return body, nil
}

func safeBODSFaresRequestError(err error) string {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) || urlErr.Err == nil {
		return fmt.Sprintf("request failed (%T)", err)
	}
	reason := urlErr.Err.Error()
	if parsedURL, parseErr := url.Parse(urlErr.URL); parseErr == nil {
		if apiKey := parsedURL.Query().Get("api_key"); apiKey != "" {
			reason = strings.ReplaceAll(reason, apiKey, "[redacted]")
			reason = strings.ReplaceAll(reason, url.QueryEscape(apiKey), "[redacted]")
		}
	}
	if strings.Contains(strings.ToLower(reason), "api_key") {
		return fmt.Sprintf("request failed (%T)", urlErr.Err)
	}
	return "request failed: " + reason
}

func (format *Fares) parseNeTExPayload(body []byte) error {
	if len(body) >= 4 && bytes.Equal(body[:4], []byte{'P', 'K', 3, 4}) {
		archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return fmt.Errorf("open NeTEx archive: %w", err)
		}
		filesParsed := 0
		for _, entry := range archive.File {
			if entry.FileInfo().IsDir() || !strings.EqualFold(filepath.Ext(entry.Name), ".xml") {
				continue
			}
			file, err := entry.Open()
			if err != nil {
				return fmt.Errorf("open archive member %q: %w", entry.Name, err)
			}
			xmlBytes, readErr := io.ReadAll(io.LimitReader(file, maximumFaresXML+1))
			closeErr := file.Close()
			if readErr != nil {
				return fmt.Errorf("read archive member %q: %w", entry.Name, readErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close archive member %q: %w", entry.Name, closeErr)
			}
			if int64(len(xmlBytes)) > maximumFaresXML {
				return fmt.Errorf("archive member %q exceeded %d bytes", entry.Name, maximumFaresXML)
			}
			if err := format.ParseFile(bytes.NewReader(xmlBytes)); err != nil {
				return fmt.Errorf("archive member %q: %w", entry.Name, err)
			}
			filesParsed++
		}
		if filesParsed == 0 {
			return fmt.Errorf("NeTEx archive contains no XML documents")
		}
		return nil
	}
	return format.ParseFile(bytes.NewReader(body))
}

func (entry faresMetadataEntry) downloadURL() string {
	for _, value := range []string{entry.DownloadURL, entry.DownloadURLCamel, entry.DataURL, entry.URL} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (entry faresMetadataEntry) identifier() string {
	var value string
	if len(entry.ID) > 0 && json.Unmarshal(entry.ID, &value) == nil && value != "" {
		return value
	}
	if len(entry.ID) > 0 {
		var number json.Number
		if json.Unmarshal(entry.ID, &number) == nil {
			return number.String()
		}
	}
	return "unknown"
}

func (entry faresMetadataEntry) namespace() string {
	return "bods-feed-" + entry.identifier()
}

func (entry faresMetadataEntry) operatorRefs() []string {
	values := append([]string(nil), entry.NOCs...)
	values = append(values, entry.OperatorNOCs...)
	values = append(values, entry.NOC...)
	values = append(values, entry.OperatorNOC...)
	values = append(values, entry.NationalOperator...)
	return uniqueStrings(values)
}
