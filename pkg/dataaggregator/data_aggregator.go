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
}

var GlobalAggregator Aggregator

func (a *Aggregator) RegisterSource(source source.DataSource) {
	a.Sources = append(a.Sources, source)

	log.Debug().Str("name", source.GetName()).Msg("Registering new Data Source")
}

func Lookup[T any](query any) (T, error) {
	var empty T

	lookupType := reflect.TypeOf(*new(T))
	if lookupType.Kind() == reflect.Pointer {
		lookupType = lookupType.Elem()
	}

	for _, dataSource := range GlobalAggregator.Sources {
		for _, supportedType := range dataSource.Supports() {
			if lookupType == supportedType {
				var returnValue any
				var returnError error

				returnValue, returnError = dataSource.Lookup(query)

				if returnError != nil && returnError.Error() == "unsupported Source for this query" {
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
