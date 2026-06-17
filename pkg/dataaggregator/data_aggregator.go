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

	// PERF(medium-risk): index of supported reflect.Type -> ordered sources supporting it,
	// built incrementally in RegisterSource. Kept in sync with Sources; the public Sources
	// field is preserved for any external readers.
	sourcesByType map[reflect.Type][]source.DataSource
}

var GlobalAggregator Aggregator

func (a *Aggregator) RegisterSource(src source.DataSource) {
	a.Sources = append(a.Sources, src)

	// PERF(medium-risk): maintain a map[reflect.Type][]DataSource index alongside the
	// Sources slice so Lookup can resolve candidate sources in O(1) instead of scanning
	// every source and its Supports() list on every call. The per-type slice preserves
	// registration order, so the "first source that doesn't return UnsupportedSourceError
	// wins" fallthrough behaviour in Lookup is unchanged (multiple sources can support the
	// same type, e.g. tfl and localdepartureboard both support []*DepartureBoard).
	// (Parameter renamed from `source` to `src` so the `source` package identifier remains
	// usable for the map's element type below.)
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

	// PERF(medium-risk): use the pre-built per-type index (ordered list of sources that
	// support lookupType) instead of the previous nested loop over all sources x their
	// Supports() types. Iteration order over the slice matches registration order, so the
	// UnsupportedSourceError fallthrough semantics are identical to the old code.
	for _, dataSource := range GlobalAggregator.sourcesByType[lookupType] {
		var returnValue any
		var returnError error

		returnValue, returnError = dataSource.Lookup(query)

		// PERF(low-risk): compare against the sentinel via errors.Is instead of matching
		// the error's string. All sources return source.UnsupportedSourceError directly
		// (verified: it is the only construction of that message and none wrap it with
		// %w), so errors.Is is semantically identical here and avoids a string allocation
		// and comparison per call.
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
