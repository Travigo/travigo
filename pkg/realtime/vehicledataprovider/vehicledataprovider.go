package vehicledataprovider

import (
	"context"
	"errors"

	"github.com/travigo/travigo/pkg/ctdf"
)

// ErrLookupDeferred means no external request was sent and the lookup may be resumed.
var ErrLookupDeferred = errors.New("vehicle data lookup deferred")

type VehicleDataProvider interface {
	GetVehicleDataBus(ctx context.Context, registrationNumber string) (*ctdf.JourneyDetailedBus, error)
}
