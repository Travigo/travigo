package realtimestore

import (
	"context"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/redis_client"
)

func UpdateLocationDescription(ctx context.Context, identifier string, description string) error {
	redis_client.Client.Set(ctx, fmt.Sprintf("realtime-journey:%s/locationdescription", identifier), description, 12*time.Hour)
	return nil
}
