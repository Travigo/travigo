package dvlaves

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"go.mongodb.org/mongo-driver/mongo"
)

const (
	ProviderID                = "gb-dvla-ves"
	CollectionName            = "dvla_ves_vehicle_cache"
	APIKeyEnvironmentVariable = "TRAVIGO_DVLA_VES_API_KEY"
	endpoint                  = "https://driver-vehicle-licensing.api.gov.uk/vehicle-enquiry/v1/vehicles"
	rateKey                   = "vehicle-data-provider:gb-dvla-ves:rate"
)

var validRegistration = regexp.MustCompile(`^[A-Z0-9]{2,7}$`)
var bodsOperatorCode = regexp.MustCompile(`^[A-Z0-9]{4}$`)
var registrationInVehicleRef = regexp.MustCompile(`^([A-Z]{2}[0-9]{2}[A-Z]{3}|[A-Z][0-9]{1,3}[A-Z]{3}|[A-Z]{3}[0-9]{1,3}[A-Z])$`)

func NormalizeRegistration(value string) string {
	registration := strings.ToUpper(strings.Join(strings.Fields(value), ""))
	if !validRegistration.MatchString(registration) {
		return ""
	}
	return registration
}

// RegistrationFromVehicleRef accepts common plate formats in BODS vehicle
// references, including the operator-prefixed form used in GTFS-RT and SIRI-VM.
func RegistrationFromVehicleRef(ref string) string {
	ref = strings.ToUpper(strings.TrimSpace(ref))
	if operator, vehicle, ok := strings.Cut(ref, "-"); ok {
		if !bodsOperatorCode.MatchString(operator) {
			return ""
		}
		ref = vehicle
	}
	registration := NormalizeRegistration(strings.ReplaceAll(ref, "_", " "))
	if !registrationInVehicleRef.MatchString(registration) {
		return ""
	}
	return registration
}

type Client struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
	cache      *mongo.Collection
	redis      *redis.Client
}

func New(apiKey string, cache *mongo.Collection, redisClient *redis.Client) *Client {
	return &Client{
		apiKey: apiKey, endpoint: endpoint, cache: cache, redis: redisClient,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// DVLA's default allocation is 2 requests per second for the entire API key.
// Space requests across all tracker replicas sharing Redis.
func (client *Client) waitForRequestSlot(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		granted, err := client.redis.SetNX(ctx, rateKey, "1", 550*time.Millisecond).Result()
		if err != nil || granted {
			return err
		}
		remaining, err := client.redis.PTTL(ctx, rateKey).Result()
		if err != nil {
			return err
		}
		if remaining > 2*time.Second {
			return fmt.Errorf("DVLA VES request cooldown is active")
		}
		if err := wait(ctx, max(remaining, 10*time.Millisecond)); err != nil {
			return err
		}
	}
}

func (client *Client) lookup(ctx context.Context, registration string) (*ctdf.JourneyDetailedBus, int, error) {
	body, err := json.Marshal(struct {
		RegistrationNumber string `json:"registrationNumber"`
	}{registration})
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("x-api-key", client.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var vehicle struct {
			RegistrationNumber string
			Make               string
			Colour             string
			FuelType           string
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&vehicle); err != nil {
			return nil, resp.StatusCode, fmt.Errorf("decode DVLA VES response: %w", err)
		}
		if NormalizeRegistration(vehicle.RegistrationNumber) != registration {
			return nil, resp.StatusCode, fmt.Errorf("DVLA VES returned a mismatched registration")
		}
		return &ctdf.JourneyDetailedBus{
			RegistrationNumber: registration,
			VehicleMake:        vehicle.Make,
			VehicleColour:      vehicle.Colour,
			PowerType:          vehicle.FuelType,
		}, resp.StatusCode, nil
	case http.StatusNotFound:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))
		return nil, resp.StatusCode, nil
	default:
		return nil, resp.StatusCode, fmt.Errorf("DVLA VES returned HTTP %d", resp.StatusCode)
	}
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
