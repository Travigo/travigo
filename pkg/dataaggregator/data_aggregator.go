package dataaggregator

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/travigo/travigo/pkg/dataaggregator/source"

	"github.com/rs/zerolog/log"
)

type Aggregator struct {
	Sources []source.DataSource

	sourcesByType map[reflect.Type][]source.DataSource
}

var GlobalAggregator Aggregator

func (a *Aggregator) RegisterSource(src source.DataSource) {
	a.Sources = append(a.Sources, src)

	if a.sourcesByType == nil {
		a.sourcesByType = make(map[reflect.Type][]source.DataSource)
	}
	for _, supportedType := range src.Supports() {
		a.sourcesByType[supportedType] = append(a.sourcesByType[supportedType], src)
	}

	log.Debug().Str("name", src.GetName()).Msg("Registering new Data Source")
}

func Lookup[T any](query any) (T, error) {
	var empty T

	lookupType := reflect.TypeOf(*new(T))
	if lookupType.Kind() == reflect.Pointer {
		lookupType = lookupType.Elem()
	}

	for _, dataSource := range GlobalAggregator.sourcesByType[lookupType] {
		var returnValue any
		var returnError error

		returnValue, returnError = dataSource.Lookup(query)

		if returnError != nil && errors.Is(returnError, source.UnsupportedSourceError) {
			continue
		}

		if returnValue == nil {
			return empty, returnError
		} else {
			return returnValue.(T), returnError
		}
	}

	return empty, fmt.Errorf("Failed to find a matching Data Source for %s", lookupType.String())
}
