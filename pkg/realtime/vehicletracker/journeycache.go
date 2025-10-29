package vehicletracker

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	redisstore "github.com/eko/gocache/store/redis/v4"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
)

var journeyStateCache *cache.Cache[*CachedRealtimeJourney]

// CachedRealtimeJourney holds the current state of a realtime journey in Redis
type CachedRealtimeJourney struct {
	PrimaryIdentifier string                       `json:"primary_identifier"`
	NextStopRef       string                       `json:"next_stop_ref"`
	DepartedStopRef   string                       `json:"departed_stop_ref"`
	Offset            time.Duration                `json:"offset"`
	LastLocation      ctdf.Location                `json:"last_location"`
	LastBearing       float64                      `json:"last_bearing"`
	LastOccupancy     ctdf.RealtimeJourneyOccupancy `json:"last_occupancy"`
	LastDBWrite       time.Time                    `json:"last_db_write"`
	LastUpdate        time.Time                    `json:"last_update"`
	IsNew             bool                         `json:"is_new"`
}

// ChangeDetectionConfig holds thresholds for determining if changes are significant
type ChangeDetectionConfig struct {
	// Minimum distance in meters before writing location update
	MinLocationChangeMeters float64
	// Minimum bearing change in degrees before writing
	MinBearingChangeDegrees float64
	// Force DB write after this duration even if no changes
	MaxTimeBetweenWrites time.Duration
	// Minimum time between any updates
	MinTimeBetweenUpdates time.Duration
}

var defaultChangeConfig = ChangeDetectionConfig{
	MinLocationChangeMeters: 25.0,  // 25 meters
	MinBearingChangeDegrees: 15.0,  // 15 degrees
	MaxTimeBetweenWrites:    5 * time.Minute,
	MinTimeBetweenUpdates:   10 * time.Second,
}

func (c *CachedRealtimeJourney) MarshalBinary() ([]byte, error) {
	return json.Marshal(c)
}

func (c *CachedRealtimeJourney) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, c)
}

// CreateJourneyStateCache initializes the Redis cache for journey states
func CreateJourneyStateCache() {
	redisStore := redisstore.NewRedis(redis_client.Client, store.WithExpiration(30*time.Minute))
	journeyStateCache = cache.New[*CachedRealtimeJourney](redisStore)
}

// GetCachedJourneyState retrieves journey state from Redis cache
func GetCachedJourneyState(ctx context.Context, journeyIdentifier string) (*CachedRealtimeJourney, error) {
	cacheKey := fmt.Sprintf("journey_state:%s", journeyIdentifier)
	cached, err := journeyStateCache.Get(ctx, cacheKey)
	if err != nil {
		return nil, err
	}
	return cached, nil
}

// SetCachedJourneyState stores journey state in Redis cache
func SetCachedJourneyState(ctx context.Context, journeyIdentifier string, state *CachedRealtimeJourney) error {
	cacheKey := fmt.Sprintf("journey_state:%s", journeyIdentifier)
	return journeyStateCache.Set(ctx, cacheKey, state)
}

// ShouldWriteToDatabase determines if changes are significant enough to write to MongoDB
func (cached *CachedRealtimeJourney) ShouldWriteToDatabase(
	newLocation ctdf.Location,
	newBearing float64,
	newOccupancy ctdf.RealtimeJourneyOccupancy,
	newNextStopRef string,
	newDepartedStopRef string,
	newOffset time.Duration,
	currentTime time.Time,
	config ChangeDetectionConfig,
) (bool, string) {
	// Always write if this is a new journey
	if cached == nil || cached.IsNew {
		return true, "new_journey"
	}

	// Check if minimum time between updates has passed
	if currentTime.Sub(cached.LastUpdate) < config.MinTimeBetweenUpdates {
		return false, "too_soon"
	}

	// Force write if max time exceeded
	if currentTime.Sub(cached.LastDBWrite) >= config.MaxTimeBetweenWrites {
		return true, "max_time_exceeded"
	}

	// Check for significant changes

	// Next stop changed (significant event)
	if cached.NextStopRef != newNextStopRef {
		return true, "next_stop_changed"
	}

	// Departed stop changed
	if cached.DepartedStopRef != newDepartedStopRef {
		return true, "departed_stop_changed"
	}

	// Offset changed significantly (already calculated as meaningful)
	if cached.Offset.Seconds() != newOffset.Seconds() {
		return true, "offset_changed"
	}

	// Occupancy changed (check if availability status or percentage changed significantly)
	if cached.LastOccupancy.OccupancyAvailable != newOccupancy.OccupancyAvailable {
		return true, "occupancy_availability_changed"
	}
	if newOccupancy.OccupancyAvailable {
		// Only check percentage if occupancy is actually available
		percentageDiff := math.Abs(float64(cached.LastOccupancy.TotalPercentageOccupancy - newOccupancy.TotalPercentageOccupancy))
		if percentageDiff >= 10.0 { // 10% threshold
			return true, fmt.Sprintf("occupancy_changed_%.0f%%", percentageDiff)
		}
	}

	// Location changed significantly
	if newLocation.Type == "Point" && cached.LastLocation.Type == "Point" {
		distance := cached.LastLocation.Distance(&newLocation)
		if distance >= config.MinLocationChangeMeters {
			return true, fmt.Sprintf("location_changed_%.1fm", distance)
		}
	} else if newLocation.Type != cached.LastLocation.Type {
		// Location type changed (e.g., from empty to Point)
		return true, "location_type_changed"
	}

	// Bearing changed significantly
	if bearingDifference(cached.LastBearing, newBearing) >= config.MinBearingChangeDegrees {
		return true, fmt.Sprintf("bearing_changed_%.1f°", bearingDifference(cached.LastBearing, newBearing))
	}

	// No significant changes
	return false, "no_significant_changes"
}

// bearingDifference calculates the smallest angle between two bearings
func bearingDifference(b1, b2 float64) float64 {
	diff := math.Abs(b1 - b2)
	if diff > 180 {
		diff = 360 - diff
	}
	return diff
}
