package stats

import (
	"bytes"
	"encoding/json"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/travigo/travigo/pkg/elastic_client"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
)

type identificationStatsTransport struct {
	t     *testing.T
	body  string
	calls int
}

func (tr *identificationStatsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.calls++
	payload, err := io.ReadAll(req.Body)
	if err != nil {
		tr.t.Fatal(err)
	}
	var query map[string]any
	if err := json.Unmarshal(payload, &query); err != nil {
		tr.t.Fatal(err)
	}
	operators := query["aggs"].(map[string]any)["operators"].(map[string]any)["aggs"].(map[string]any)
	check := func(aggs map[string]any) {
		tr.t.Helper()
		sum := aggs["weighted_count"].(map[string]any)["sum"].(map[string]any)
		if sum["field"] != "SamplingWeight" || sum["missing"] != float64(1) {
			tr.t.Fatalf("missing sampling/historical weights: %s", payload)
		}
	}
	check(operators)
	check(operators["success"].(map[string]any)["aggs"].(map[string]any))
	return &http.Response{StatusCode: 200, Header: http.Header{"X-Elastic-Product": []string{"Elasticsearch"}}, Body: io.NopCloser(strings.NewReader(tr.body))}, nil
}
func TestIdentificationRatesUseWeightedTotalsForEveryWindow(t *testing.T) {
	transport := &identificationStatsTransport{t: t, body: `{"aggregations":{"operators":{"buckets":[{"key":"sampled","doc_count":2,"weighted_count":{"value":110},"success":{"buckets":[{"key_as_string":"true","doc_count":1,"weighted_count":{"value":100}},{"key_as_string":"false","doc_count":1,"weighted_count":{"value":10}}]}},{"key":"legacy","doc_count":10,"weighted_count":{"value":10},"success":{"buckets":[{"key_as_string":"true","doc_count":8,"weighted_count":{"value":8}},{"key_as_string":"false","doc_count":2,"weighted_count":{"value":2}}]}}]}}}`}
	client, err := elasticsearch.NewClient(elasticsearch.Config{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	old := elastic_client.Client
	elastic_client.Client = client
	defer func() { elastic_client.Client = old }()
	got := GetIdentificationRateStats(nil)
	if transport.calls != 3 {
		t.Fatalf("got %d windows", transport.calls)
	}
	for name, want := range map[string]float64{"sampled": 100.0 / 110, "legacy": 0.8, "total": 0.9} {
		rates := got.TotalRate
		if name != "total" {
			rates = got.Operators[name]
		}
		if rates == nil {
			t.Fatalf("missing %s", name)
		}
		for _, actual := range []float64{rates.LastDayRate, rates.LastWeekRate, rates.LastMonthRate} {
			if math.Abs(actual-want) > 1e-9 {
				t.Fatalf("%s rate %f, want %f", name, actual, want)
			}
		}
	}
	// Historical fields stay missing in stored records; the query supplies weight one.
	encoded, _ := json.Marshal(identificationWeightAggregation())
	if !bytes.Contains(encoded, []byte(`"missing":1`)) {
		t.Fatal("legacy fallback removed")
	}
}
func TestIdentificationRateEmptyWindow(t *testing.T) {
	if got := identificationRate(0, 0); got != 0 || math.IsNaN(got) {
		t.Fatal("empty window produced invalid rate")
	}
}
