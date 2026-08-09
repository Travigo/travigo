package departuregraph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

const departuresPath = "/v1/departures"

type departuresRequest struct {
	PrimaryStopRef string   `json:"primaryStopRef"`
	StopRefs       []string `json:"stopRefs"`
	ServiceDate    string   `json:"serviceDate"`
}

type departuresResponse struct {
	Journeys []*ctdf.Journey `json:"journeys"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

func (c *Client) JourneysForStop(ctx context.Context, stop *ctdf.Stop, serviceDate time.Time) ([]*ctdf.Journey, error) {
	if c == nil || c.baseURL == "" || stop == nil {
		return nil, fmt.Errorf("departure graph client is not configured")
	}

	payload, err := json.Marshal(departuresRequest{
		PrimaryStopRef: stop.PrimaryIdentifier,
		StopRefs:       stop.GetAllStopIDs(),
		ServiceDate:    serviceDate.Format(ctdf.YearMonthDayFormat),
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+departuresPath, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("departure graph returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}

	var result departuresResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode departure graph response: %w", err)
	}
	return result.Journeys, nil
}
