package query

import (
	"context"

	"github.com/travigo/travigo/pkg/ctdf"
	"go.mongodb.org/mongo-driver/bson"
)

type Service struct {
	PrimaryIdentifier string
}

func (s *Service) ToBson() bson.M {
	if s.PrimaryIdentifier != "" {
		return bson.M{"primaryidentifier": s.PrimaryIdentifier}
	}

	return nil
}

type ServicesByStop struct {
	Stop *ctdf.Stop
}

// ServicesByStops resolves service references for multiple stops in one batch.
// Context is optional; callers performing a long-running index can pass their
// cancellation/deadline through to the underlying source.
type ServicesByStops struct {
	Stops   []*ctdf.Stop
	Context context.Context
}
