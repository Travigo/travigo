package source

import (
	"context"
	"errors"
	"reflect"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/dataaggregator/query"
	"github.com/britbus/britbus/pkg/database"
)

type DatabaseLookupSource struct {
}

func (d DatabaseLookupSource) GetName() string {
	return "Database Lookup"
}

func (d DatabaseLookupSource) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(ctdf.Stop{}),
	}
}

func (d DatabaseLookupSource) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case query.Stop:
		stopQuery := q.(query.Stop)

		stopsCollection := database.GetCollection("stops")
		var stop *ctdf.Stop
		stopsCollection.FindOne(context.Background(), stopQuery.ToBson()).Decode(&stop)

		if stop == nil {
			return nil, errors.New("Could not find a matching Stop")
		} else {
			return stop, nil
		}
	}

	return nil, errors.New("Unable to lookup")
}
