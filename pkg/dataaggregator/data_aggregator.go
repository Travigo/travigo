package dataaggregator

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/britbus/britbus/pkg/dataaggregator/source"
	"github.com/rs/zerolog/log"
)

type Aggregator struct {
	Sources []DataSource
}

var globalAggregator Aggregator

func GlobalSetup() {
	globalAggregator = Aggregator{}

	globalAggregator.RegisterSource(source.DatabaseLookupSource{})
	globalAggregator.RegisterSource(source.LocalDepartureBoardSource{})
	globalAggregator.RegisterSource(source.NationalRailSource{})
}

func (a *Aggregator) RegisterSource(source DataSource) {
	a.Sources = append(a.Sources, source)

	log.Debug().Str("name", source.GetName()).Msg("Registering new Data Source")
}

func Lookup[T any](query any) (T, error) {
	var empty T

	lookupType := reflect.TypeOf(*new(T))
	if lookupType.Kind() == reflect.Pointer {
		lookupType = lookupType.Elem()
	}

	for _, source := range globalAggregator.Sources {
		for _, supportedType := range source.Supports() {
			if lookupType == supportedType {
				var returnValue any
				var returnError error

				returnValue, returnError = source.Lookup(query)

				if returnError != nil && returnError.Error() == "Unsupported Source for this query" {
					continue
				}

				if returnValue == nil {
					return empty, returnError
				} else {
					return returnValue.(T), returnError
				}
			}
		}
	}

	return empty, errors.New(fmt.Sprintf("Failed to find a matching Data Source for %s", lookupType.String()))
}
