package netex

import (
	"errors"
	"net/url"
	"strings"
	"testing"
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
