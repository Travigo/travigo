package dataaggregator

import (
	"errors"
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

	databaseLookupSource := source.DatabaseLookupSource{}
	globalAggregator.RegisterSource(databaseLookupSource)
}

func (a *Aggregator) RegisterSource(source DataSource) {
	a.Sources = append(a.Sources, source)

	log.Debug().Str("name", source.GetName()).Msg("Registering new Data Source")
}

func Lookup[T any](query any) (T, error) {
	var empty T

	for _, source := range globalAggregator.Sources {
		matches := false

		lookupType := reflect.TypeOf(*new(T))
		if lookupType.Kind() == reflect.Pointer {
			lookupType = lookupType.Elem()
		}

		for _, supportedType := range source.Supports() {
			if lookupType == supportedType {
				matches = true
				break
			}
		}

		if matches {
			var returnValue any
			var returnError error

			returnValue, returnError = source.Lookup(query)

			if returnValue == nil {
				return empty, returnError
			} else {
				return returnValue.(T), returnError
			}
		}
	}

	return empty, errors.New("Failed to find a matching Data Source for type")
}
