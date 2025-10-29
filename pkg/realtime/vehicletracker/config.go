package vehicletracker

import (
	"os"
	"strconv"
	"time"
)

// GetChangeDetectionConfig returns the change detection configuration
// from environment variables or defaults
func GetChangeDetectionConfig() ChangeDetectionConfig {
	config := defaultChangeConfig

	// Allow overriding via environment variables
	if val := os.Getenv("REALTIME_MIN_LOCATION_CHANGE_METERS"); val != "" {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil {
			config.MinLocationChangeMeters = parsed
		}
	}

	if val := os.Getenv("REALTIME_MIN_BEARING_CHANGE_DEGREES"); val != "" {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil {
			config.MinBearingChangeDegrees = parsed
		}
	}

	if val := os.Getenv("REALTIME_MAX_TIME_BETWEEN_WRITES"); val != "" {
		if parsed, err := time.ParseDuration(val); err == nil {
			config.MaxTimeBetweenWrites = parsed
		}
	}

	if val := os.Getenv("REALTIME_MIN_TIME_BETWEEN_UPDATES"); val != "" {
		if parsed, err := time.ParseDuration(val); err == nil {
			config.MinTimeBetweenUpdates = parsed
		}
	}

	return config
}
