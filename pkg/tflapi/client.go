package tflapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Client struct {
	AppKey     string
	HTTPClient *http.Client
	Requests   <-chan time.Time
}

type Line struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type RouteSequence struct {
	LineStrings       []string           `json:"lineStrings"`
	OrderedLineRoutes []OrderedLineRoute `json:"orderedLineRoutes"`
	Stations          []Station          `json:"stations"`
}

type OrderedLineRoute struct {
	Name      string   `json:"name"`
	NaptanIDs []string `json:"naptanIds"`
}

type Station struct {
	ID        string  `json:"id"`
	Latitude  float64 `json:"lat"`
	Longitude float64 `json:"lon"`
}

func (client *Client) Lines(ctx context.Context, modeID string) ([]Line, error) {
	var lines []Line
	err := client.getJSON(ctx, fmt.Sprintf("https://api.tfl.gov.uk/Line/Mode/%s", url.PathEscape(modeID)), &lines)
	return lines, err
}

func (client *Client) RouteSequence(ctx context.Context, lineID string, direction string) (RouteSequence, error) {
	var sequence RouteSequence
	err := client.getJSON(ctx, fmt.Sprintf("https://api.tfl.gov.uk/Line/%s/Route/Sequence/%s", url.PathEscape(lineID), url.PathEscape(direction)), &sequence)
	return sequence, err
}

func (client *Client) getJSON(ctx context.Context, endpoint string, target any) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	query := parsed.Query()
	query.Set("app_key", client.AppKey)
	parsed.RawQuery = query.Encode()

	httpClient := client.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	for attempt := 0; attempt < 3; attempt++ {
		if client.Requests != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-client.Requests:
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("user-agent", "curl/7.54.1")

		resp, err := httpClient.Do(req)
		if err != nil {
			if attempt == 2 {
				return err
			}
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			if err := json.Unmarshal(body, target); err != nil {
				return fmt.Errorf("decode TfL response: %w", err)
			}
			return nil
		}
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < http.StatusInternalServerError {
			return fmt.Errorf("TfL returned %s", resp.Status)
		}
		if attempt == 2 {
			return fmt.Errorf("TfL returned %s after retries", resp.Status)
		}

		retryDelay := time.Duration(attempt+1) * time.Second
		if seconds, parseErr := strconv.Atoi(resp.Header.Get("Retry-After")); parseErr == nil && seconds > 0 {
			retryDelay = time.Duration(seconds) * time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}
	}
	return nil
}
