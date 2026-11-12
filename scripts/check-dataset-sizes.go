// Run from the repository root: go run ./scripts/check-dataset-sizes.go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

const (
	smallLimit = 10000
	largeLimit = 100000
)

type entry struct {
	id, size string
}

type result struct {
	report datasets.DataImportReport
	status string
	err    error
}

func expectedSize(objects int) string {
	switch {
	case objects < smallLimit:
		return "small"
	case objects < largeLimit:
		return "medium"
	default:
		return "large"
	}
}

func fetchReport(client *http.Client, base, id string) result {
	resp, err := client.Get(base + "/dataset/" + url.PathEscape(id) + "/import_report")
	if err != nil {
		return result{err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		var body struct{ Error string }
		if json.NewDecoder(resp.Body).Decode(&body) == nil && body.Error == "Import report could not be found" {
			return result{status: "missing-report"}
		}
	}
	if resp.StatusCode != http.StatusOK {
		return result{err: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}

	var report datasets.DataImportReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		return result{err: err}
	}
	if report.DatasetIdentifier != id || report.CreationDateTime.IsZero() {
		return result{err: fmt.Errorf("unexpected dataset identifier or missing report date")}
	}
	return result{report: report}
}

func main() {
	base := flag.String("base-url", "https://api.travigo.app/core/datasources", "datasources API URL")
	all := flag.Bool("all", false, "include matching datasets and enrichment reports")
	flag.Parse()

	var entries []entry
	for _, source := range datasets.LoadDataSources() {
		for _, dataset := range source.Datasets {
			switch dataset.ImportDestination {
			case datasets.ImportDestinationRealtimeQueue, datasets.ImportDestinationSpecificRunner:
				continue
			}
			size := dataset.DatasetSize
			if size == "" {
				size = "small"
			}
			entries = append(entries, entry{source.Identifier + "-" + dataset.Identifier, size})
		}
	}
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.id, b.id) })

	client := &http.Client{Timeout: 15 * time.Second}
	results := make([]result, len(entries))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				results[i] = fetchReport(client, strings.TrimRight(*base, "/"), entries[i].id)
			}
		}()
	}
	for i := range entries {
		jobs <- i
	}
	close(jobs)
	workers.Wait()

	fmt.Println("dataset\tobjects\tconfigured\texpected\treport_date\tstatus")
	var matched, mismatched, missing, enrichment, failed int
	for i, dataset := range entries {
		res := results[i]
		if res.err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", dataset.id, res.err)
			failed++
			continue
		}
		if res.status == "missing-report" {
			fmt.Printf("%s\t-\t%s\t-\t-\tmissing-report\n", dataset.id, dataset.size)
			missing++
			continue
		}

		report := res.report
		objects := report.ImportedStops + report.ImportedStopGroups + report.ImportedServices +
			report.ImportedJourneys + report.ImportedJourneyTracks + report.ImportedOperators +
			report.ImportedOperationGroups
		expected := expectedSize(objects)
		status := "match"
		switch {
		case dataset.size == "enrichment" || dataset.size == "journey-enrichment":
			expected = "-"
			status = "enrichment"
			enrichment++
		case dataset.size != expected:
			status = "mismatch"
			mismatched++
		default:
			matched++
		}
		if *all || status == "mismatch" {
			fmt.Printf("%s\t%d\t%s\t%s\t%s\t%s\n", dataset.id, objects, dataset.size, expected,
				report.CreationDateTime.UTC().Format(time.RFC3339), status)
		}
	}

	fmt.Fprintf(os.Stderr, "%d matched, %d mismatched, %d missing reports, %d enrichment reports, %d errors\n",
		matched, mismatched, missing, enrichment, failed)
	if failed > 0 {
		os.Exit(2)
	}
	if mismatched > 0 {
		os.Exit(1)
	}
}
