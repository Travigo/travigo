package realtimestore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
)

type storedVehicleLocation struct {
	Location ctdf.Location `json:"location"`
	Bearing  float64       `json:"bearing"`
}

func UpdateLocationDescription(ctx context.Context, identifier string, description string) error {
	redis_client.Client.Set(ctx, fmt.Sprintf("realtime-journey:%s/locationdescription", identifier), description, 12*time.Hour)
	return nil
}

func UpdateLocation(ctx context.Context, identifier string, location ctdf.Location, bearing float64) error {
	locationJSON, err := json.Marshal(storedVehicleLocation{
		Location: location,
		Bearing:  bearing,
	})
	if err != nil {
		return err
	}

	redis_client.Client.Set(ctx, fmt.Sprintf("realtime-journey:%s/location", identifier), locationJSON, 12*time.Hour)
	return nil
}
