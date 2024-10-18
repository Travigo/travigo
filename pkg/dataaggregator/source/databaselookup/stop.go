package databaselookup

import (
	"context"
	"errors"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
)

func (s Source) StopQuery(stopQuery query.Stop) (*ctdf.Stop, error) {
	stopsCollection := database.GetCollection("stops")
	var stop *ctdf.Stop
	stopsCollection.FindOne(context.Background(), stopQuery.ToBson()).Decode(&stop)

	if stop == nil {
		return nil, errors.New("could not find a matching Stop")
	} else {
		return stop, nil
	}
}
