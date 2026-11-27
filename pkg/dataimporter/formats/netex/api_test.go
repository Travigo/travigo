package netex

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func TestSafeBODSFaresRequestErrorIncludesCauseWithoutLeakingAPIKey(t *testing.T) {
	const key = "private-test-key"
	requestURL := "https://data.bus-data.dft.gov.uk/api/v1/fares/dataset/?api_key=" + url.QueryEscape(key)
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "normal transport error",
			err:  &url.Error{Op: "Get", URL: requestURL, Err: errors.New("context deadline exceeded")},
			want: "context deadline exceeded",
		},
		{
			name: "error text contains request URL",
			err:  &url.Error{Op: "Get", URL: requestURL, Err: errors.New("failed request to " + requestURL)},
			want: "request failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := safeBODSFaresRequestError(test.err)
			if !strings.Contains(got, test.want) {
				t.Errorf("safeBODSFaresRequestError() = %q, want it to contain %q", got, test.want)
			}
			if strings.Contains(got, key) || strings.Contains(strings.ToLower(got), "api_key") {
				t.Errorf("safeBODSFaresRequestError() leaked request credentials: %q", got)
			}
		})
	}
}

func TestBODSFareFeedRecordFilterScopesFailedFeedCleanup(t *testing.T) {
	filter := bodsFareFeedRecordFilter("bods-fares", "candidate-v2", "bods-feed-feed.1875")
	if filter["datasetid"] != "bods-fares" || filter["fareversionid"] != "candidate-v2" {
		t.Fatalf("fare cleanup filter = %#v, want the candidate fare version", filter)
	}
	sourceIdentifier, ok := filter["sourceidentifier"].(bson.M)
	if !ok || sourceIdentifier["$regex"] != `^bods-feed-feed\.1875:` {
		t.Fatalf("source identifier filter = %#v, want escaped feed namespace prefix", filter["sourceidentifier"])
	}
}

func TestParseNeTExPayloadReportsHTMLResponse(t *testing.T) {
	body := []byte(`<!DOCTYPE html><html><head><title>Problem with the service</title></head><body><h1>Sorry, there is a problem with the service</h1></body></html>`)
	err := (&Fares{}).parseNeTExPayload(body)
	if err == nil || !strings.Contains(err.Error(), "HTML page instead of NeTEx") {
		t.Fatalf("expected an actionable HTML response error, got %v", err)
	}
}
