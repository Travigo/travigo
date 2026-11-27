// Regenerates data/datasources/fr-transport-data-gouv.yaml from the official
// transport.data.gouv.fr catalogue. Existing YAML is read as a curation
// overlay so reviewed provider grouping, stable identifiers, source overrides,
// feed sizes, and realtime links survive each refresh.
//
// Run from the repository root:
//
//	go run ./scripts/generate-fr-transport-data-gouv.go
//
// For an offline refresh from a saved API response:
//
//	go run ./scripts/generate-fr-transport-data-gouv.go -catalog-json /path/to/datasets.json
package main

import (
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

	"gopkg.in/yaml.v3"
)

const (
	catalogURL = "https://transport.data.gouv.fr/api/datasets"
	outPath    = "data/datasources/fr-transport-data-gouv.yaml"
)

var (
	pageSlugPattern         = regexp.MustCompile(`https://transport\.data\.gouv\.fr/datasets/([a-zA-Z0-9-]+)`)
	officialResourcePattern = regexp.MustCompile(`(?m)^  # Official resource: https://transport\.data\.gouv\.fr/resources/(\d+)[ \t]*\n  - identifier: ([a-zA-Z0-9-]+)$`)
	resourceIDPattern       = regexp.MustCompile(`-(\d+)$`)
	separatorPattern        = regexp.MustCompile(`(?m)^---\s*$`)
)

type catalogDataset struct {
	Type      string            `json:"type"`
	Slug      string            `json:"slug"`
	Title     string            `json:"title"`
	PageURL   string            `json:"page_url"`
	Publisher catalogPublisher  `json:"publisher"`
	Resources []catalogResource `json:"resources"`
}

type catalogPublisher struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type catalogResource struct {
	ID          int64    `json:"id"`
	Title       string   `json:"title"`
	Format      string   `json:"format"`
	OriginalURL string   `json:"original_url"`
	URL         string   `json:"url"`
	PageURL     string   `json:"page_url"`
	Type        string   `json:"type"`
	Available   bool     `json:"is_available"`
	Features    []string `json:"features"`
}

type existingSource struct {
	Identifier string `yaml:"identifier"`
	Region     string `yaml:"region"`
	Provider   struct {
		Name    string `yaml:"name"`
		Website string `yaml:"website"`
	} `yaml:"provider"`
	Datasets []existingDataset `yaml:"datasets"`
}

type existingDataset struct {
	Identifier    string `yaml:"identifier"`
	Format        string `yaml:"format"`
	Source        string `yaml:"source"`
	DatasetSize   string `yaml:"datasetsize"`
	LinkedDataset string `yaml:"linkeddataset"`
	Supported     struct {
		Realtime bool `yaml:"realtimejourneys"`
		Alerts   bool `yaml:"servicealerts"`
	} `yaml:"supportedobjects"`
}

type curation struct {
	byResourceID     map[int64]existingDataset
	groupByResource  map[int64]string
	resourceOrder    map[int64]int
	groupBySlug      map[string]string
	pageOrder        map[string]int
	groupsByID       map[string]existingSource
	groupOrder       map[string]int
	groupByProvider  map[string]string
	publisherGroupID map[string]string
	ambiguousPublish map[string]bool
}

type outputGroup struct {
	id            string
	name          string
	website       string
	existing      bool
	order         int
	firstAPIOrder int
	records       []*outputRecord
}

type outputRecord struct {
	dataset catalogDataset
	order   int
	feeds   []*outputFeed
	notes   []catalogResource
}

type outputFeed struct {
	resource catalogResource
	format   string
	id       string
	source   string
	size     string
	link     string
	realtime bool
	alerts   bool
	order    int
}

func main() {
	catalogJSON := flag.String("catalog-json", "", "read a saved transport.data.gouv.fr API response instead of fetching it")
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

	datasets, err := readCatalog(*catalogJSON, root)
	check(err)
	generated, stats, err := generate(datasets, curated)
	check(err)
	if stats.groups == 0 || stats.feeds == 0 {
		check(errors.New("catalogue produced no datasource groups or feeds; refusing to overwrite the YAML"))
	}
	check(os.WriteFile(outFile, generated, 0o644))
	relativeOutput, _ := filepath.Rel(root, outFile)
	fmt.Printf("Wrote %s: %d provider groups, %d active feeds, %d commented alternatives from %d public-transit records.\n",
		relativeOutput, stats.groups, stats.feeds, stats.notes, stats.records)
}

func repositoryRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("could not locate generator source")
	}
	return filepath.Dir(filepath.Dir(file)), nil
}

func readCatalog(inputPath, root string) ([]catalogDataset, error) {
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
		request.Header.Set("User-Agent", "travigo-fr-catalog-generator/1.0")
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
	var datasets []catalogDataset
	if err := json.Unmarshal(body, &datasets); err != nil {
		return nil, fmt.Errorf("decode catalogue response: %w", err)
	}
	if len(datasets) == 0 {
		return nil, errors.New("catalogue response contained no datasets; refusing to overwrite the YAML")
	}
	return datasets, nil
}

func readCuration(path string) (curation, error) {
	result := curation{
		byResourceID:     make(map[int64]existingDataset),
		groupByResource:  make(map[int64]string),
		resourceOrder:    make(map[int64]int),
		groupBySlug:      make(map[string]string),
		pageOrder:        make(map[string]int),
		groupsByID:       make(map[string]existingSource),
		groupOrder:       make(map[string]int),
		groupByProvider:  make(map[string]string),
		publisherGroupID: make(map[string]string),
		ambiguousPublish: make(map[string]bool),
	}
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	if os.IsNotExist(err) {
		return result, nil
	}

	sections := separatorPattern.Split(string(body), -1)
	for _, section := range sections {
		var source existingSource
		if err := yaml.Unmarshal([]byte(section), &source); err != nil {
			return result, fmt.Errorf("decode existing French datasource YAML: %w", err)
		}
		if source.Identifier == "" {
			continue
		}
		if _, exists := result.groupsByID[source.Identifier]; !exists {
			result.groupOrder[source.Identifier] = len(result.groupOrder)
		}
		result.groupsByID[source.Identifier] = source
		if source.Provider.Name != "" {
			result.groupByProvider[source.Provider.Name] = source.Identifier
		}
		for index, dataset := range source.Datasets {
			id := resourceID(dataset.Identifier)
			if id == 0 {
				id = sectionResourceID(section, dataset.Identifier)
			}
			if id == 0 {
				continue
			}
			result.byResourceID[id] = dataset
			result.groupByResource[id] = source.Identifier
			result.resourceOrder[id] = index
		}
		for _, match := range pageSlugPattern.FindAllStringSubmatch(section, -1) {
			slug := match[1]
			if _, exists := result.groupBySlug[slug]; !exists {
				result.pageOrder[slug] = len(result.pageOrder)
			}
			result.groupBySlug[slug] = source.Identifier
		}
	}
	return result, nil
}

func generate(datasets []catalogDataset, curated curation) ([]byte, generationStats, error) {
	stats := generationStats{}
	// Learn which existing provider group a publisher usually belongs to. Some
	// regional publishers serve multiple independent authorities, so only use
	// publisher IDs that map to one reviewed group.
	for _, dataset := range datasets {
		groupID := curated.groupBySlug[dataset.Slug]
		if groupID == "" || dataset.Publisher.ID == "" {
			continue
		}
		previous := curated.publisherGroupID[dataset.Publisher.ID]
		if previous != "" && previous != groupID {
			curated.ambiguousPublish[dataset.Publisher.ID] = true
		} else {
			curated.publisherGroupID[dataset.Publisher.ID] = groupID
		}
	}
	for publisherID := range curated.ambiguousPublish {
		delete(curated.publisherGroupID, publisherID)
	}

	groups := make(map[string]*outputGroup)
	var newPublisherGroup = make(map[string]string)
	var newTitleGroup = make(map[string]string)
	knownExclusions := map[string]bool{
		"horaires-sncf": true, // configured in fr-sncf.yaml
	}
	excludedNetex := map[int64]bool{
		83580: true, // Aérobus A01 is already present in the IDFM GTFS feed.
		83044: true, // Aérobus A04 is already present in the IDFM GTFS feed.
	}

	for index, dataset := range datasets {
		if dataset.Type != "public-transit" || dataset.Slug == "" || knownExclusions[dataset.Slug] {
			continue
		}
		stats.records++
		groupID, name, _ := curatedGroup(dataset, curated)
		if groupID == "" {
			key := ""
			if dataset.Publisher.ID != "" && !curated.ambiguousPublish[dataset.Publisher.ID] {
				key = "publisher:" + dataset.Publisher.ID
				groupID = newPublisherGroup[key]
			} else if dataset.Title != "" {
				key = "title:" + dataset.Title
				groupID = newTitleGroup[key]
			}
			if groupID == "" {
				groupID = "fr-" + safeSlug(dataset.Slug)
				if key == "" {
					key = "slug:" + dataset.Slug
				}
				if strings.HasPrefix(key, "publisher:") {
					newPublisherGroup[key] = groupID
				} else if strings.HasPrefix(key, "title:") {
					newTitleGroup[key] = groupID
				}
			}
			if name == "" {
				if strings.HasPrefix(key, "publisher:") {
					name = dataset.Publisher.Name
				} else {
					name = dataset.Title
				}
			}
			if name == "" {
				name = dataset.Publisher.Name
			}
		}
		if name == "" {
			if previous, ok := curated.groupsByID[groupID]; ok {
				name = previous.Provider.Name
			}
		}
		if name == "" {
			name = dataset.Title
		}

		group := groups[groupID]
		if group == nil {
			group = &outputGroup{id: groupID, name: name, firstAPIOrder: index, order: len(curated.groupOrder) + index}
			if old, ok := curated.groupsByID[groupID]; ok {
				group.existing = true
				group.name = old.Provider.Name
				group.website = old.Provider.Website
				group.order = curated.groupOrder[groupID]
			}
			groups[groupID] = group
		}
		record := &outputRecord{dataset: dataset, order: index}
		group.records = append(group.records, record)

		hasGTFS := hasFormat(dataset.Resources, "gtfs")
		for _, resource := range dataset.Resources {
			if resource.ID == 0 {
				continue
			}
			if feedFormat, ok := supportedFormat(resource); ok {
				if isWrongPybusFeed(dataset, resource) {
					// The API currently points Pybus at Noirmoutier's feed.
					record.notes = append(record.notes, resource)
					continue
				}
				if feedFormat == "netex" && hasGTFS {
					continue
				}
				if feedFormat == "netex" && excludedNetex[resource.ID] {
					record.notes = append(record.notes, resource)
					continue
				}
				old, hadOld := curated.byResourceID[resource.ID]
				if !hadOld && !resource.Available {
					continue
				}
				if isAlternativeResource(resource) && hasGTFS && !hadOld {
					continue
				}
				feed := buildFeed(resource, feedFormat, old, hadOld)
				record.feeds = append(record.feeds, feed)
				continue
			}
			if isCatalogNote(resource) {
				record.notes = append(record.notes, resource)
			}
		}
	}

	// Keep only catalogue records with importer feeds or explicitly documented
	// alternatives. A disappeared API record or resource is omitted on refresh.
	for id, group := range groups {
		kept := group.records[:0]
		for _, record := range group.records {
			if len(record.feeds) == 0 && len(record.notes) == 0 {
				continue
			}
			kept = append(kept, record)
		}
		group.records = kept
		if len(group.records) == 0 {
			delete(groups, id)
			continue
		}
		if group.website == "" || (isCatalogDatasetURL(group.website) && !catalogueContainsWebsite(group.records, group.website)) {
			group.website = group.records[0].dataset.PageURL
		}
		if group.name == "" {
			group.name = group.records[0].dataset.Title
		}
		sort.SliceStable(group.records, func(i, j int) bool {
			a, b := group.records[i], group.records[j]
			if a.order != b.order {
				return a.order < b.order
			}
			return a.dataset.Slug < b.dataset.Slug
		})
	}

	// Preserve the existing page order and append new catalogue pages in API order.
	for _, group := range groups {
		sort.SliceStable(group.records, func(i, j int) bool {
			a, b := group.records[i], group.records[j]
			aOld, aKnown := curated.pageOrder[a.dataset.Slug]
			bOld, bKnown := curated.pageOrder[b.dataset.Slug]
			if aKnown != bKnown {
				return aKnown
			}
			if aKnown && aOld != bOld {
				return aOld < bOld
			}
			if a.order != b.order {
				return a.order < b.order
			}
			return a.dataset.Slug < b.dataset.Slug
		})
		// Keep previously configured feeds in their original order; newly added
		// resources follow in the order supplied by the catalogue.
		for _, record := range group.records {
			sort.SliceStable(record.feeds, func(i, j int) bool {
				a, b := record.feeds[i], record.feeds[j]
				aOld, aKnown := curated.resourceOrder[a.resource.ID]
				bOld, bKnown := curated.resourceOrder[b.resource.ID]
				if aKnown != bKnown {
					return aKnown
				}
				if aKnown && aOld != bOld {
					return aOld < bOld
				}
				return a.order < b.order
			})
		}
	}

	var orderedGroups []*outputGroup
	for _, group := range groups {
		orderedGroups = append(orderedGroups, group)
	}
	sort.SliceStable(orderedGroups, func(i, j int) bool {
		a, b := orderedGroups[i], orderedGroups[j]
		if a.existing != b.existing {
			return a.existing
		}
		if a.existing && a.order != b.order {
			return a.order < b.order
		}
		if a.firstAPIOrder != b.firstAPIOrder {
			return a.firstAPIOrder < b.firstAPIOrder
		}
		return a.id < b.id
	})

	activeIDs := make(map[string]bool)
	for _, group := range orderedGroups {
		for _, record := range group.records {
			for _, feed := range record.feeds {
				activeIDs[group.id+"-"+feed.id] = true
			}
		}
	}
	for _, group := range orderedGroups {
		for _, record := range group.records {
			for _, feed := range record.feeds {
				if !isRealtime(feed.format) {
					continue
				}
				if feed.link != "" && activeIDs[feed.link] {
					continue
				}
				if candidate := linkedSchedule(group, record); candidate != nil {
					feed.link = group.id + "-" + candidate.id
				}
			}
		}
	}

	var builder strings.Builder
	builder.WriteString("# Generated from the transport.data.gouv.fr public-transit API.\n")
	builder.WriteString("# Regenerate with: go run ./scripts/generate-fr-transport-data-gouv.go\n")
	builder.WriteString("# Repeated provider names are consolidated; existing source IDs and reviewed importer choices are retained.\n")
	builder.WriteString("# SNCF remains in its dedicated datasource file.\n")
	builder.WriteString("# Airline SSIM feeds and records without a supported feed are excluded.\n")
	builder.WriteString("# NeTEx alternatives to a GTFS schedule and the two overlapping Aérobus feeds remain disabled.\n")
	builder.WriteString("# The Aérobus A01 and A04 routes are already included in IDFM GTFS (IDFM:C02179 and IDFM:C02723).\n")
	builder.WriteString("# Unsupported SIRI profiles and SIRI Lite resources remain commented for review.\n")
	builder.WriteString("# The Pybus GTFS-RT endpoint stays disabled until the publisher confirms it is correct.\n")
	for _, group := range orderedGroups {
		stats.groups++
		builder.WriteString("---\n")
		builder.WriteString("identifier: " + group.id + "\n")
		builder.WriteString("region: fr\n")
		builder.WriteString("provider:\n")
		builder.WriteString("  name: " + yamlString(group.name) + "\n")
		builder.WriteString("  website: " + yamlString(group.website) + "\n")
		if len(group.records) > 1 {
			builder.WriteString("  # Other grouped catalogue pages:\n")
			for _, record := range group.records {
				if record.dataset.PageURL != "" && record.dataset.PageURL != group.website {
					builder.WriteString("  # - " + record.dataset.PageURL + "\n")
				}
			}
		}
		builder.WriteString("datasets:\n")
		if group.id == "fr-navettes-aeroport-paris-beauvais-aerobus" {
			builder.WriteString("  # Both lines are already scheduled by fr-ile-de-france-mobilites-gtfs-schedule-80921.\n")
		}
		for _, record := range group.records {
			if record.dataset.PageURL != "" && (record.dataset.PageURL != group.website || len(group.records) > 1) {
				builder.WriteString("  # Catalogue record: " + record.dataset.PageURL + "\n")
			}
			for _, feed := range record.feeds {
				stats.feeds++
				writeActiveFeed(&builder, feed)
			}
			for _, resource := range record.notes {
				stats.notes++
				writeCatalogNote(&builder, group, record, resource)
			}
		}
	}
	return []byte(builder.String()), stats, nil
}

type generationStats struct {
	groups  int
	feeds   int
	notes   int
	records int
}

func curatedGroup(dataset catalogDataset, curated curation) (string, string, bool) {
	if id := curated.groupBySlug[dataset.Slug]; id != "" {
		if old, ok := curated.groupsByID[id]; ok {
			return id, old.Provider.Name, true
		}
		return id, "", true
	}
	for _, resource := range dataset.Resources {
		if id := curated.groupByResource[resource.ID]; id != "" {
			if old, ok := curated.groupsByID[id]; ok {
				return id, old.Provider.Name, true
			}
			return id, "", true
		}
	}
	for _, name := range []string{dataset.Title, dataset.Publisher.Name} {
		if id := curated.groupByProvider[name]; id != "" {
			return id, curated.groupsByID[id].Provider.Name, true
		}
	}
	if id := curated.publisherGroupID[dataset.Publisher.ID]; id != "" {
		return id, curated.groupsByID[id].Provider.Name, true
	}
	return "", "", false
}

func hasFormat(resources []catalogResource, format string) bool {
	for _, resource := range resources {
		if strings.EqualFold(strings.TrimSpace(resource.Format), format) {
			return true
		}
	}
	return false
}

func supportedFormat(resource catalogResource) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(resource.Format)) {
	case "gtfs":
		return "gtfs-schedule", true
	case "gtfs-rt":
		return "gtfs-realtime", true
	case "netex":
		return "netex", true
	case "siri":
		title := strings.ToLower(resource.Title + " " + resource.OriginalURL)
		switch {
		case strings.Contains(title, "siri vm"), strings.Contains(title, "siri-vm"), strings.Contains(title, "siri-vm"):
			return "eu-siri-vm", true
		case strings.Contains(title, "siri sx"), strings.Contains(title, "siri-sx"), strings.Contains(title, "situation exchange"):
			return "eu-siri-sx", true
		}
	}
	return "", false
}

func isCatalogNote(resource catalogResource) bool {
	format := strings.ToLower(strings.TrimSpace(resource.Format))
	return format == "siri lite" || format == "siri"
}

func isAlternativeResource(resource catalogResource) bool {
	title := strings.ToLower(resource.Title)
	return strings.Contains(title, "gtfs modifié") || strings.Contains(title, "modified gtfs")
}

func isWrongPybusFeed(dataset catalogDataset, resource catalogResource) bool {
	endpoint := strings.ToLower(resource.OriginalURL + " " + resource.URL)
	return dataset.Slug == "gtfs-pybus-le-reseau-urbain-de-parthenay" &&
		(resource.ID == 82620 || strings.Contains(endpoint, "/noirmoutiers/gtfs-rt"))
}

func isWrongPybusNote(group *outputGroup, resource catalogResource) bool {
	endpoint := strings.ToLower(resource.OriginalURL + " " + resource.URL)
	return group.id == "fr-gtfs-pybus-le-reseau-urbain-de-parthenay" &&
		(resource.ID == 82620 || strings.Contains(endpoint, "/noirmoutiers/gtfs-rt"))
}

func buildFeed(resource catalogResource, format string, old existingDataset, hadOld bool) *outputFeed {
	id := format + "-" + strconv.FormatInt(resource.ID, 10)
	if hadOld && (old.Identifier == format || strings.HasPrefix(old.Identifier, format+"-")) {
		id = old.Identifier
	}
	source := firstNonEmpty(resource.OriginalURL, resource.URL)
	if hadOld {
		source = refreshedSource(old.Source, resource)
	}
	feed := &outputFeed{resource: resource, format: format, id: id, source: source, order: int(resource.ID)}
	if format == "gtfs-schedule" || format == "netex" {
		feed.size = "small"
		if hadOld {
			feed.size = old.DatasetSize
			if feed.size == "" {
				feed.size = "small"
			}
		}
	} else {
		feed.realtime, feed.alerts = realtimeFeatures(resource, format)
		if hadOld {
			feed.link = old.LinkedDataset
			feed.realtime = old.Supported.Realtime
			feed.alerts = old.Supported.Alerts
		}
	}
	return feed
}

func realtimeFeatures(resource catalogResource, format string) (bool, bool) {
	if format == "eu-siri-vm" {
		return true, false
	}
	if format == "eu-siri-sx" {
		return false, true
	}
	journeys, alerts := false, false
	for _, feature := range resource.Features {
		switch strings.ToLower(feature) {
		case "trip_updates", "vehicle_positions", "realtimejourneys":
			journeys = true
		case "alerts", "service_alerts", "servicealerts":
			alerts = true
		}
	}
	if len(resource.Features) > 0 {
		return journeys, alerts
	}
	title := strings.ToLower(resource.Title)
	if strings.Contains(title, "alert") || strings.Contains(title, "perturbation") || strings.Contains(title, "message") {
		alerts = true
	}
	if strings.Contains(title, "vehicle") || strings.Contains(title, "position") || strings.Contains(title, "horaire") || strings.Contains(title, "trip update") || strings.Contains(title, "temps réel") || strings.Contains(title, "temps reel") {
		journeys = true
	}
	if !journeys && !alerts {
		journeys = true
	}
	return journeys, alerts
}

func refreshedSource(previous string, resource catalogResource) string {
	current := firstNonEmpty(resource.OriginalURL, resource.URL)
	if previous == "" || current == "" {
		return current
	}
	if previous == resource.URL || previous == current {
		return previous
	}
	oldURL, oldErr := url.Parse(previous)
	newURL, newErr := url.Parse(current)
	if oldErr == nil && newErr == nil && oldURL.Host != "" && strings.EqualFold(oldURL.Host, newURL.Host) {
		return current
	}
	// A different host usually means a reviewed proxy or equivalent endpoint.
	return previous
}

func linkedSchedule(group *outputGroup, record *outputRecord) *outputFeed {
	for _, feed := range record.feeds {
		if feed.format == "gtfs-schedule" {
			return feed
		}
	}
	for _, candidateRecord := range group.records {
		for _, feed := range candidateRecord.feeds {
			if feed.format == "gtfs-schedule" {
				return feed
			}
		}
	}
	return nil
}

func isRealtime(format string) bool {
	return format == "gtfs-realtime" || format == "eu-siri-vm" || format == "eu-siri-sx"
}

func writeActiveFeed(builder *strings.Builder, feed *outputFeed) {
	if feed.resource.Title != "" {
		builder.WriteString("  # " + cleanComment(feed.resource.Title) + "\n")
	}
	if page := firstNonEmpty(feed.resource.PageURL, fmt.Sprintf("https://transport.data.gouv.fr/resources/%d", feed.resource.ID)); page != "" {
		builder.WriteString("  # Official resource: " + page + "\n")
	}
	builder.WriteString("  - identifier: " + feed.id + "\n")
	builder.WriteString("    format: " + feed.format + "\n")
	builder.WriteString("    source: " + yamlString(feed.source) + "\n")
	if feed.size != "" {
		builder.WriteString("    datasetsize: " + feed.size + "\n")
	}
	if feed.link != "" {
		builder.WriteString("    linkeddataset: " + feed.link + "\n")
	}
	if isRealtime(feed.format) {
		builder.WriteString("    importdestination: realtime-queue\n")
		builder.WriteString("    supportedobjects:\n")
		if feed.realtime {
			builder.WriteString("      realtimejourneys: true\n")
		}
		if feed.alerts {
			builder.WriteString("      servicealerts: true\n")
		}
		return
	}
	builder.WriteString("    supportedobjects:\n")
	builder.WriteString("      stops: true\n")
	builder.WriteString("      services: true\n")
	builder.WriteString("      journeys: true\n")
	builder.WriteString("      operators: true\n")
}

func writeCatalogNote(builder *strings.Builder, group *outputGroup, record *outputRecord, resource catalogResource) {
	format := strings.ToLower(strings.TrimSpace(resource.Format))
	if isWrongPybusNote(group, resource) {
		builder.WriteString(fmt.Sprintf("  # GTFS-RT Réseau PYBUS (Official resource: https://transport.data.gouv.fr/resources/%d)\n", resource.ID))
		builder.WriteString("  # The catalog points this at the Noirmoutier endpoint, also listed for Gratibus;\n")
		builder.WriteString("  # keep disabled until the publisher confirms the correct PYBUS feed URL.\n")
		builder.WriteString("  # - identifier: gtfs-realtime-" + strconv.FormatInt(resource.ID, 10) + "\n")
		builder.WriteString("  #   format: gtfs-realtime\n")
		builder.WriteString("  #   source: " + yamlString(resource.OriginalURL) + "\n")
		if schedule := linkedSchedule(group, record); schedule != nil {
			builder.WriteString("  #   linkeddataset: " + group.id + "-" + schedule.id + "\n")
		} else {
			builder.WriteString("  #   linkeddataset: fr-gtfs-pybus-le-reseau-urbain-de-parthenay-gtfs-schedule-82619\n")
		}
		return
	}
	label := "SIRI"
	todo := "TODO: Confirm the SIRI profile; current SIRI importers handle VM and SX only."
	if format == "siri lite" {
		label = "SIRI Lite"
		todo = "TODO: Add a SIRI Lite importer before enabling these realtime resources."
	} else if strings.Contains(strings.ToLower(resource.Title+" "+resource.OriginalURL), "siri sm") || strings.Contains(strings.ToLower(resource.Title+" "+resource.OriginalURL), "siri-sm") {
		todo = "TODO: SIRI Stop Monitoring is not supported; current SIRI importers handle VM and SX."
	} else if format == "netex" {
		label = "NeTEx"
		todo = ""
	}
	if todo != "" {
		builder.WriteString("  # " + todo + "\n")
	}
	builder.WriteString(fmt.Sprintf("  # Resource %d: %s [%s]\n", resource.ID, cleanComment(resource.Title), label))
	if resource.OriginalURL != "" {
		builder.WriteString("  # Source: " + resource.OriginalURL + "\n")
	}
	page := firstNonEmpty(resource.PageURL, fmt.Sprintf("https://transport.data.gouv.fr/resources/%d", resource.ID))
	if page != "" {
		builder.WriteString("  # Catalogue resource: " + page + "\n")
	}
	if resource.ID == 83044 {
		builder.WriteString("  # GTFS route: IDFM:C02723\n")
	} else if resource.ID == 83580 {
		builder.WriteString("  # GTFS route: IDFM:C02179\n")
	}
	if label == "SIRI Lite" || label == "NeTEx" {
		formatValue := "eu-siri-lite"
		identifierFormat := "siri-lite"
		resourceSource := resource.OriginalURL
		if label == "NeTEx" {
			formatValue = "netex"
			identifierFormat = "netex"
			resourceSource = firstNonEmpty(resource.URL, resource.OriginalURL)
		}
		builder.WriteString("  # - identifier: " + identifierFormat + "-" + strconv.FormatInt(resource.ID, 10) + "\n")
		builder.WriteString("  #   format: " + formatValue + "\n")
		if resourceSource != "" {
			builder.WriteString("  #   source: " + yamlString(resourceSource) + "\n")
		}
		if label == "NeTEx" {
			builder.WriteString("  #   unpackbundle: zip\n")
			builder.WriteString("  #   customconfig:\n")
			builder.WriteString("  #     timezone: Europe/Paris\n")
			builder.WriteString("  #   supportedobjects:\n")
			builder.WriteString("  #     operators: true\n")
			builder.WriteString("  #     stops: true\n")
			builder.WriteString("  #     stopgroups: true\n")
			builder.WriteString("  #     services: true\n")
			builder.WriteString("  #     journeys: true\n")
		}
	}
}

func catalogueContainsWebsite(records []*outputRecord, website string) bool {
	for _, record := range records {
		if record.dataset.PageURL == website {
			return true
		}
	}
	return false
}

func isCatalogDatasetURL(website string) bool {
	parsed, err := url.Parse(website)
	return err == nil && strings.EqualFold(parsed.Host, "transport.data.gouv.fr") && strings.Contains(parsed.Path, "/datasets/")
}

func sectionResourceID(section, identifier string) int64 {
	for _, match := range officialResourcePattern.FindAllStringSubmatch(section, -1) {
		if match[2] == identifier {
			id, _ := strconv.ParseInt(match[1], 10, 64)
			return id
		}
	}
	return 0
}

func resourceID(identifier string) int64 {
	match := resourceIDPattern.FindStringSubmatch(identifier)
	if len(match) != 2 {
		return 0
	}
	id, _ := strconv.ParseInt(match[1], 10, 64)
	return id
}

func safeSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var result strings.Builder
	separator := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
			separator = false
		} else if !separator && result.Len() > 0 {
			result.WriteByte('-')
			separator = true
		}
	}
	return strings.Trim(result.String(), "-")
}

func yamlString(value string) string { return strconv.Quote(value) }

func cleanComment(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " "))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "generate French transport catalogue:", err)
		os.Exit(1)
	}
}
