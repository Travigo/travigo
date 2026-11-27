// Regenerates data/datasources/jp-odpt.yaml from ODPT's public GTFS resource
// index. Existing dataset identifiers and importer settings are a curation
// overlay; unmatched feeds already in the file remain as curated supplements.
//
// Run from the repository root:
//
//	go run ./scripts/generate-jp-odpt.go
//
// For an offline refresh from a saved API response:
//
//	go run ./scripts/generate-jp-odpt.go -catalog-json /path/to/resources.json
package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

const (
	catalogURL = "https://members-portal.odpt.org/api/v1/resources?format=gtfs"
	outPath    = "data/datasources/jp-odpt.yaml"
)

var (
	managedFeedPattern = regexp.MustCompile(`(?m)^# ODPT resource key: ([^\r\n]+)\r?\n- identifier: ([a-zA-Z0-9-]+)[ \t]*$`)
	idPattern          = regexp.MustCompile(`[^a-z0-9]+`)
)

type catalogOrganization struct {
	Label    string           `json:"label"`
	NameEN   string           `json:"name_en"`
	NameJA   string           `json:"name_ja"`
	URLEN    string           `json:"url_en"`
	URLJA    string           `json:"url_ja"`
	Datasets []catalogDataset `json:"datasets"`
}

type catalogDataset struct {
	Label           string            `json:"label"`
	NameEN          string            `json:"name_en"`
	NameJA          string            `json:"name_ja"`
	ExplainEN       string            `json:"explain_en"`
	ExplainJA       string            `json:"explain_ja"`
	FormatType      string            `json:"format_type"`
	LicenseType     string            `json:"license_type"`
	LicenseExplain  string            `json:"license_explain"`
	ModeList        []string          `json:"mode_list"`
	DataResources   []catalogResource `json:"dataresource"`
	LegacyResources []catalogResource `json:"datasource"`
	TripUpdate      catalogRealtime   `json:"trip_update"`
	VehiclePosition catalogRealtime   `json:"vehicle_position"`
	Alert           catalogRealtime   `json:"alert"`
}

type catalogResource struct {
	URL                   string `json:"url"`
	StartAt               string `json:"start_at"`
	UploadedAt            string `json:"uploaded_at"`
	FeedStartDate         string `json:"feed_start_date"`
	FeedEndDate           string `json:"feed_end_date"`
	IsAvailableThisPeriod bool   `json:"is_feed_available_period"`
}

type catalogRealtime struct {
	IsOperated bool   `json:"is_operated"`
	URL        string `json:"url"`
}

type sourceFile struct {
	Identifier string            `yaml:"identifier"`
	Region     string            `yaml:"region"`
	Provider   sourceProvider    `yaml:"provider"`
	Datasets   []existingDataset `yaml:"datasets"`
}

type sourceProvider struct {
	Name    string `yaml:"name"`
	Website string `yaml:"website"`
}

type existingDataset struct {
	Identifier        string                 `yaml:"identifier"`
	Format            string                 `yaml:"format"`
	Source            string                 `yaml:"source"`
	DatasetSize       string                 `yaml:"datasetsize"`
	CustomConfig      map[string]interface{} `yaml:"customconfig"`
	LinkedDataset     string                 `yaml:"linkeddataset"`
	ImportDestination string                 `yaml:"importdestination"`
	Supported         supportedObjects       `yaml:"supportedobjects"`
}

type supportedObjects struct {
	Stops            bool `yaml:"stops"`
	Services         bool `yaml:"services"`
	Journeys         bool `yaml:"journeys"`
	Operators        bool `yaml:"operators"`
	RealtimeJourneys bool `yaml:"realtimejourneys"`
	ServiceAlerts    bool `yaml:"servicealerts"`
}

type curatedEntry struct {
	dataset existingDataset
	key     string
	order   int
	managed bool
}

type curation struct {
	entries []curatedEntry
	byKey   map[string]curatedEntry
	byPath  map[string]curatedEntry
}

type outputFeed struct {
	dataset existingDataset
	key     string
	label   string
	order   int
}

type generationStats struct {
	organizations    int
	catalogSchedules int
	catalogRealtime  int
	activeSchedules  int
	realtimeFeeds    int
	inactiveDatasets int
	unsupportedURLs  int
	excludedLicenses []string
}

func main() {
	catalogJSON := flag.String("catalog-json", "", "read a saved ODPT resource index instead of fetching it")
	output := flag.String("output", outPath, "generated YAML output path")
	flag.Parse()

	root, err := repositoryRoot()
	check(err)
	outFile := *output
	if !filepath.IsAbs(outFile) {
		outFile = filepath.Join(root, outFile)
	}
	curated, err := readCuration(outFile)
	check(err)
	organizations, err := readCatalog(*catalogJSON, root)
	check(err)
	generated, stats, err := generate(organizations, curated)
	check(err)
	if stats.catalogSchedules == 0 || stats.catalogRealtime == 0 {
		check(errors.New("ODPT index produced no active schedules or realtime feeds; refusing to overwrite the YAML"))
	}
	if err := os.WriteFile(outFile, generated, 0o644); err != nil {
		check(err)
	}
	relativeOutput, _ := filepath.Rel(root, outFile)
	fmt.Printf("Wrote %s: %d schedules, %d realtime feeds from %d organizations (%d inactive datasets and %d unsupported URLs skipped).\n",
		relativeOutput, stats.activeSchedules, stats.realtimeFeeds, stats.organizations, stats.inactiveDatasets, stats.unsupportedURLs)
	if len(stats.excludedLicenses) > 0 {
		fmt.Printf("Left %d active datasets out for license review.\n", len(stats.excludedLicenses))
	}
}

func repositoryRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("could not locate generator source")
	}
	return filepath.Dir(filepath.Dir(file)), nil
}

func readCatalog(inputPath, root string) ([]catalogOrganization, error) {
	var body []byte
	var err error
	if inputPath != "" {
		if !filepath.IsAbs(inputPath) {
			inputPath = filepath.Join(root, inputPath)
		}
		body, err = os.ReadFile(inputPath)
	} else {
		client := &http.Client{Timeout: 90 * time.Second}
		request, requestErr := http.NewRequest(http.MethodGet, catalogURL, nil)
		if requestErr != nil {
			return nil, requestErr
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", "travigo-jp-odpt-catalog-generator/1.0")
		response, requestErr := client.Do(request)
		if requestErr != nil {
			return nil, fmt.Errorf("fetch %s: %w", catalogURL, requestErr)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch %s: HTTP %s", catalogURL, response.Status)
		}
		body, err = io.ReadAll(response.Body)
	}
	if err != nil {
		return nil, err
	}
	var organizations []catalogOrganization
	if err := json.Unmarshal(body, &organizations); err != nil {
		return nil, fmt.Errorf("decode ODPT resource index: %w", err)
	}
	if len(organizations) == 0 {
		return nil, errors.New("ODPT resource index contained no organizations; refusing to overwrite the YAML")
	}
	return organizations, nil
}

func readCuration(path string) (curation, error) {
	result := curation{byKey: make(map[string]curatedEntry), byPath: make(map[string]curatedEntry)}
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	if os.IsNotExist(err) {
		return result, nil
	}
	var source sourceFile
	if err := yaml.Unmarshal(body, &source); err != nil {
		return result, fmt.Errorf("decode existing ODPT datasource YAML: %w", err)
	}
	managedByID := make(map[string]string)
	for _, match := range managedFeedPattern.FindAllStringSubmatch(string(body), -1) {
		managedByID[match[2]] = match[1]
	}
	for index, dataset := range source.Datasets {
		entry := curatedEntry{dataset: dataset, key: managedByID[dataset.Identifier], order: index, managed: managedByID[dataset.Identifier] != ""}
		result.entries = append(result.entries, entry)
		if entry.key != "" {
			result.byKey[entry.key] = entry
		}
		pathKey := feedPathKey(dataset.Format, dataset.Source)
		if pathKey != "" {
			result.byPath[pathKey] = entry
		}
	}
	return result, nil
}

func generate(organizations []catalogOrganization, curated curation) ([]byte, generationStats, error) {
	stats := generationStats{organizations: len(organizations)}
	var schedules []outputFeed
	var realtime []outputFeed
	consumed := make(map[string]bool)
	usedIDs := make(map[string]bool)
	for _, entry := range curated.entries {
		usedIDs[entry.dataset.Identifier] = true
	}
	seenIdentities := make(map[string]bool)

	for _, organization := range organizations {
		for _, dataset := range organization.Datasets {
			if !strings.Contains(strings.ToLower(dataset.FormatType), "gtfs") {
				continue
			}
			resource, ok := latestActiveResource(dataset.resources())
			if !ok {
				stats.inactiveDatasets++
				continue
			}
			if !supportedLicense(dataset.LicenseType) {
				label := displayName(organization, dataset) + " (" + firstNonempty(dataset.LicenseType, "license not specified") + ")"
				if explanation := singleLine(dataset.LicenseExplain); explanation != "" {
					label += ": " + explanation
				}
				stats.excludedLicenses = append(stats.excludedLicenses, label)
				continue
			}
			source, err := cleanFeedURL(resource.URL)
			if err != nil || !isODPTFileURL(source) {
				stats.unsupportedURLs++
				continue
			}
			identity := scheduleIdentity(organization, dataset)
			if seenIdentities[identity] {
				identity += ":" + shortHash(feedPathKey("gtfs-schedule", source))
			}
			seenIdentities[identity] = true
			key := "schedule:" + identity
			pathKey := feedPathKey("gtfs-schedule", source)
			old, found := curated.byKey[key]
			if !found {
				old, found = curated.byPath[pathKey]
			}
			identifier := ""
			order := len(curated.entries)
			if found {
				identifier = old.dataset.Identifier
				order = old.order
				consumed[identifier] = true
			} else {
				identifier = uniqueID(defaultScheduleID(organization, dataset), key, usedIDs)
			}
			usedIDs[identifier] = true
			config := existingDataset{
				Identifier:  identifier,
				Format:      "gtfs-schedule",
				Source:      source,
				DatasetSize: inferredSize(dataset.ModeList),
				Supported:   supportedObjects{Stops: true, Services: true, Journeys: true, Operators: true},
			}
			if found {
				preserveCuration(&config, old.dataset)
				config.Source = source
			}
			label := displayName(organization, dataset)
			schedules = append(schedules, outputFeed{dataset: config, key: key, label: label, order: order})
			stats.catalogSchedules++

			for _, item := range []struct {
				name string
				data catalogRealtime
			}{{"trip_update", dataset.TripUpdate}, {"vehicle_position", dataset.VehiclePosition}, {"alert", dataset.Alert}} {
				if !item.data.IsOperated || item.data.URL == "" {
					continue
				}
				realtimeURL, err := cleanFeedURL(item.data.URL)
				if err != nil || !isODPTRealtimeURL(realtimeURL) {
					stats.unsupportedURLs++
					continue
				}
				realtimeKey := "realtime:" + identity + ":" + item.name
				oldRealtime, matched := curated.byKey[realtimeKey]
				if !matched {
					oldRealtime, matched = curated.byPath[feedPathKey("gtfs-realtime", realtimeURL)]
				}
				realtimeID := ""
				realtimeOrder := len(curated.entries)
				if matched {
					realtimeID = oldRealtime.dataset.Identifier
					realtimeOrder = oldRealtime.order
					consumed[realtimeID] = true
				} else {
					realtimeID = uniqueID(defaultRealtimeID(organization, dataset, item.name), realtimeKey, usedIDs)
				}
				usedIDs[realtimeID] = true
				realtimeConfig := existingDataset{
					Identifier:        realtimeID,
					Format:            "gtfs-realtime",
					Source:            realtimeURL,
					LinkedDataset:     "jp-odpt-" + config.Identifier,
					ImportDestination: "realtime-queue",
				}
				switch item.name {
				case "trip_update":
					realtimeConfig.Supported.RealtimeJourneys = true
				case "vehicle_position":
					realtimeConfig.Supported.RealtimeJourneys = true
				case "alert":
					realtimeConfig.Supported.ServiceAlerts = true
				}
				if matched {
					preserveCuration(&realtimeConfig, oldRealtime.dataset)
					realtimeConfig.Source = realtimeURL
					realtimeConfig.LinkedDataset = "jp-odpt-" + config.Identifier
				}
				realtimeLabel := label + " - " + endpointLabel(item.name)
				realtime = append(realtime, outputFeed{dataset: realtimeConfig, key: realtimeKey, label: realtimeLabel, order: realtimeOrder})
				stats.catalogRealtime++
			}
		}
	}

	for _, entry := range curated.entries {
		if !entry.managed && !consumed[entry.dataset.Identifier] {
			feed := outputFeed{dataset: entry.dataset, order: entry.order}
			switch entry.dataset.Format {
			case "gtfs-schedule":
				schedules = append(schedules, feed)
			case "gtfs-realtime":
				realtime = append(realtime, feed)
			}
		}
	}
	if len(schedules) == 0 {
		return nil, stats, errors.New("ODPT index produced no active schedules; refusing to overwrite the YAML")
	}
	if len(realtime) == 0 {
		return nil, stats, errors.New("ODPT index produced no active realtime feeds; refusing to overwrite the YAML")
	}
	if err := validateUniqueIdentifiers(schedules, realtime); err != nil {
		return nil, stats, err
	}
	stats.activeSchedules = len(schedules)
	stats.realtimeFeeds = len(realtime)
	sortOutput(schedules)
	sortOutput(realtime)
	return render(schedules, realtime, stats.excludedLicenses), stats, nil
}

func (d catalogDataset) resources() []catalogResource {
	if len(d.DataResources) > 0 {
		return d.DataResources
	}
	return d.LegacyResources
}

func latestActiveResource(resources []catalogResource) (catalogResource, bool) {
	var selected catalogResource
	found := false
	for _, resource := range resources {
		if !resource.IsAvailableThisPeriod || strings.TrimSpace(resource.URL) == "" {
			continue
		}
		if !found || resourceIsNewer(resource, selected) {
			selected = resource
			found = true
		}
	}
	return selected, found
}

func resourceIsNewer(candidate, selected catalogResource) bool {
	for _, pair := range [][2]string{
		{candidate.UploadedAt, selected.UploadedAt},
		{candidate.StartAt, selected.StartAt},
		{candidate.FeedStartDate, selected.FeedStartDate},
	} {
		candidateTime := parseResourceDate(pair[0])
		selectedTime := parseResourceDate(pair[1])
		if candidateTime.After(selectedTime) {
			return true
		}
		if selectedTime.After(candidateTime) {
			return false
		}
	}
	return candidate.URL > selected.URL
}

func parseResourceDate(value string) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed
	}
	return time.Time{}
}

func cleanFeedURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", err
	}
	query.Del("acl:consumerKey")
	parsed.RawQuery = query.Encode()
	if parsed.Scheme != "https" || parsed.Hostname() == "" {
		return "", fmt.Errorf("not an HTTPS URL: %q", raw)
	}
	return parsed.String(), nil
}

func isODPTFileURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && isODPTHost(parsed.Hostname()) && strings.HasPrefix(parsed.Path, "/api/v4/files/")
}

func isODPTRealtimeURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && isODPTHost(parsed.Hostname()) && strings.HasPrefix(parsed.Path, "/api/v4/gtfs/realtime/")
}

func isODPTHost(host string) bool {
	return strings.EqualFold(host, "api.odpt.org") || strings.EqualFold(host, "api-public.odpt.org")
}

func feedPathKey(format, raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Path == "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "api-public.odpt.org" {
		host = "api.odpt.org"
	}
	return format + ":" + host + strings.TrimRight(parsed.EscapedPath(), "/")
}

func scheduleIdentity(organization catalogOrganization, dataset catalogDataset) string {
	return identityPart(organization.Label, organization.NameEN, organization.NameJA) + ":" + identityPart(dataset.Label, dataset.NameEN, dataset.NameJA)
}

func identityPart(values ...string) string {
	for _, value := range values {
		if slug := slugify(value); slug != "" {
			return slug
		}
	}
	return "unknown-" + shortHash(strings.Join(values, ":"))
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var ascii strings.Builder
	for _, r := range value {
		if r > unicode.MaxASCII {
			ascii.WriteByte('-')
			continue
		}
		ascii.WriteRune(r)
	}
	return strings.Trim(idPattern.ReplaceAllString(ascii.String(), "-"), "-")
}

func defaultScheduleID(organization catalogOrganization, dataset catalogDataset) string {
	org := identityPart(organization.Label, organization.NameEN, organization.NameJA)
	data := identityPart(dataset.Label, dataset.NameEN, dataset.NameJA)
	return "schedule-" + org + "-" + data
}

func defaultRealtimeID(organization catalogOrganization, dataset catalogDataset, endpoint string) string {
	return "realtime-" + strings.TrimPrefix(defaultScheduleID(organization, dataset), "schedule-") + "-" + strings.ReplaceAll(endpoint, "_", "-")
}

func uniqueID(base, key string, used map[string]bool) string {
	if !used[base] {
		return base
	}
	for length := 6; length <= 40; length += 2 {
		candidate := base + "-" + shortHashN(key, length)
		if !used[candidate] {
			return candidate
		}
	}
	fullHash := shortHashN(key, 40)
	candidate := base + "-" + fullHash
	for suffix := 2; used[candidate]; suffix++ {
		candidate = base + "-" + fullHash + "-" + strconv.Itoa(suffix)
	}
	return candidate
}

func shortHash(value string) string {
	return shortHashN(value, 8)
}

func shortHashN(value string, length int) string {
	digest := sha1.Sum([]byte(value))
	return hex.EncodeToString(digest[:])[:length]
}

func inferredSize(modes []string) string {
	for _, mode := range modes {
		if strings.EqualFold(mode, "bus") {
			return "medium"
		}
	}
	return "small"
}

func supportedLicense(license string) bool {
	switch strings.TrimSpace(license) {
	case "ODPT基本", "ODPT Basic", "CC BY 4.0", "CC0":
		return true
	default:
		return false
	}
}

func displayName(organization catalogOrganization, dataset catalogDataset) string {
	organizationName := firstNonempty(organization.NameEN, organization.NameJA, organization.Label)
	datasetName := firstNonempty(dataset.NameEN, dataset.NameJA, dataset.Label)
	return strings.TrimSpace(organizationName + " - " + datasetName)
}

func endpointLabel(endpoint string) string {
	switch endpoint {
	case "trip_update":
		return "Trip updates"
	case "vehicle_position":
		return "Vehicle positions"
	case "alert":
		return "Service alerts"
	default:
		return endpoint
	}
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "ODPT dataset"
}

func preserveCuration(target *existingDataset, curated existingDataset) {
	if curated.Format != "" {
		target.Format = curated.Format
	}
	if curated.DatasetSize != "" {
		target.DatasetSize = curated.DatasetSize
	}
	if curated.LinkedDataset != "" {
		target.LinkedDataset = curated.LinkedDataset
	}
	if curated.ImportDestination != "" {
		target.ImportDestination = curated.ImportDestination
	}
	if len(curated.CustomConfig) > 0 {
		target.CustomConfig = curated.CustomConfig
	}
	if curated.Supported != (supportedObjects{}) {
		target.Supported = curated.Supported
	}
}

func validateUniqueIdentifiers(groups ...[]outputFeed) error {
	seen := make(map[string]bool)
	for _, group := range groups {
		for _, feed := range group {
			if feed.dataset.Identifier == "" {
				return errors.New("generated ODPT feed has no identifier")
			}
			if seen[feed.dataset.Identifier] {
				return fmt.Errorf("duplicate ODPT dataset identifier %q", feed.dataset.Identifier)
			}
			seen[feed.dataset.Identifier] = true
		}
	}
	return nil
}

func sortOutput(feeds []outputFeed) {
	sort.Slice(feeds, func(i, j int) bool {
		if feeds[i].order != feeds[j].order {
			return feeds[i].order < feeds[j].order
		}
		return feeds[i].dataset.Identifier < feeds[j].dataset.Identifier
	})
}

func render(schedules, realtime []outputFeed, excludedLicenses []string) []byte {
	var out strings.Builder
	out.WriteString("# Generated from the ODPT public GTFS resource index.\n")
	out.WriteString("# Regenerate with: go run ./scripts/generate-jp-odpt.go\n")
	out.WriteString("# Existing dataset IDs and reviewed importer settings are retained when feeds match.\n")
	out.WriteString("# Unmatched ODPT API feeds already in this file remain as curated supplements.\n")
	out.WriteString("---\n")
	out.WriteString("identifier: jp-odpt\nregion: jp\nprovider:\n  name: ODPT\n  website: \"https://www.odpt.org\"\n")
	out.WriteString("sourceauthentication:\n  query:\n    \"acl:consumerKey\": TRAVIGO_JP_ODPT_API_KEY\ndatasets:\n")
	for _, feed := range schedules {
		writeFeed(&out, feed)
	}
	out.WriteString("\n")
	for _, feed := range realtime {
		writeFeed(&out, feed)
	}
	if len(excludedLicenses) > 0 {
		excluded := append([]string(nil), excludedLicenses...)
		sort.Strings(excluded)
		out.WriteString("\n# Active ODPT GTFS datasets omitted pending review of non-standard licensing:\n")
		for _, dataset := range excluded {
			fmt.Fprintf(&out, "# - %s\n", singleLine(dataset))
		}
	}
	return []byte(out.String())
}

func writeFeed(out *strings.Builder, feed outputFeed) {
	if feed.key != "" {
		if feed.label != "" {
			fmt.Fprintf(out, "# %s\n", singleLine(feed.label))
		}
		fmt.Fprintf(out, "# ODPT resource key: %s\n", feed.key)
	}
	dataset := feed.dataset
	fmt.Fprintf(out, "- identifier: %s\n", strconv.Quote(dataset.Identifier))
	fmt.Fprintf(out, "  format: %s\n", strconv.Quote(dataset.Format))
	fmt.Fprintf(out, "  source: %s\n", strconv.Quote(dataset.Source))
	if dataset.DatasetSize != "" {
		fmt.Fprintf(out, "  datasetsize: %s\n", strconv.Quote(dataset.DatasetSize))
	}
	if len(dataset.CustomConfig) > 0 {
		out.WriteString("  customconfig:\n")
		keys := make([]string, 0, len(dataset.CustomConfig))
		for key := range dataset.CustomConfig {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(out, "    %s: %s\n", strconv.Quote(key), strconv.Quote(fmt.Sprint(dataset.CustomConfig[key])))
		}
	}
	if dataset.LinkedDataset != "" {
		fmt.Fprintf(out, "  linkeddataset: %s\n", strconv.Quote(dataset.LinkedDataset))
	}
	if dataset.ImportDestination != "" {
		fmt.Fprintf(out, "  importdestination: %s\n", strconv.Quote(dataset.ImportDestination))
	}
	if dataset.Supported != (supportedObjects{}) {
		out.WriteString("  supportedobjects:\n")
		writeSupported(out, "stops", dataset.Supported.Stops)
		writeSupported(out, "services", dataset.Supported.Services)
		writeSupported(out, "journeys", dataset.Supported.Journeys)
		writeSupported(out, "operators", dataset.Supported.Operators)
		writeSupported(out, "realtimejourneys", dataset.Supported.RealtimeJourneys)
		writeSupported(out, "servicealerts", dataset.Supported.ServiceAlerts)
	}
}

func writeSupported(out *strings.Builder, name string, supported bool) {
	if supported {
		fmt.Fprintf(out, "    %s: true\n", name)
	}
}

func singleLine(value string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(value, "#", "")), " ")
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
