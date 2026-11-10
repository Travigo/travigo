package databaselookup

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/stopservices"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	stopServicesLookupTimeout = 20 * time.Second
	stopServicesLookupChunk   = 500
)

// ServicesByStopQuery is retained for single-stop callers. It uses the same
// materialized batch implementation as list and index callers.
func (s Source) ServicesByStopQuery(q query.ServicesByStop) ([]*ctdf.Service, error) {
	if q.Stop == nil {
		return nil, fmt.Errorf("load services for stop: stop is nil")
	}
	services, err := s.ServicesByStopsQuery(query.ServicesByStops{
		Stops: []*ctdf.Stop{q.Stop},
	})
	if err != nil {
		return nil, err
	}
	return services[q.Stop.PrimaryIdentifier], nil
}

// ServicesByStopsQuery returns services keyed by each stop's primary
// identifier. It gathers the complete raw stop-reference set first and asks
// the materialized relation once, then resolves all referenced services in
// bounded MongoDB chunks.
func (s Source) ServicesByStopsQuery(q query.ServicesByStops) (map[string][]*ctdf.Service, error) {
	ctx := q.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, stopServicesLookupTimeout)
	defer cancel()

	return s.materializedServicesByStops(ctx, q.Stops)
}

func (s Source) materializedServicesByStops(ctx context.Context, stops []*ctdf.Stop) (map[string][]*ctdf.Service, error) {
	result := make(map[string][]*ctdf.Service, len(stops))
	stopIDsByPrimary := make(map[string][]string, len(stops))
	allStopIDs := make(map[string]struct{})
	for _, stop := range stops {
		if stop == nil || stop.PrimaryIdentifier == "" {
			continue
		}
		stopIDs := stop.GetAllStopIDs()
		seenStopIDs := make(map[string]struct{}, len(stopIDsByPrimary[stop.PrimaryIdentifier])+len(stopIDs))
		for _, existingID := range stopIDsByPrimary[stop.PrimaryIdentifier] {
			seenStopIDs[existingID] = struct{}{}
		}
		result[stop.PrimaryIdentifier] = []*ctdf.Service{}
		for _, stopID := range stopIDs {
			if _, exists := seenStopIDs[stopID]; exists {
				continue
			}
			seenStopIDs[stopID] = struct{}{}
			stopIDsByPrimary[stop.PrimaryIdentifier] = append(stopIDsByPrimary[stop.PrimaryIdentifier], stopID)
			allStopIDs[stopID] = struct{}{}
		}
	}
	if len(allStopIDs) == 0 {
		return result, nil
	}
	if database.Instance == nil || database.Instance.Database == nil {
		return nil, fmt.Errorf("load materialized stop services: database is unavailable")
	}

	stopRefs := make([]string, 0, len(allStopIDs))
	for stopRef := range allStopIDs {
		stopRefs = append(stopRefs, stopRef)
	}
	servicesByRawStopRef, err := stopservices.BatchForStops(ctx, database.Instance.Database, stopRefs)
	if err != nil {
		return nil, fmt.Errorf("load materialized stop services: %w", err)
	}

	// Collect service references across the whole stop batch, and remember the
	// references contributed by each raw stop ID so aliases and platforms map
	// back to the primary stop that is returned to callers.
	serviceRefSet := make(map[string]struct{})
	serviceRefsByPrimary := make(map[string][]string, len(stopIDsByPrimary))
	for primaryID, stopIDs := range stopIDsByPrimary {
		seen := make(map[string]struct{})
		for _, stopID := range stopIDs {
			for _, serviceRef := range servicesByRawStopRef[stopID] {
				if serviceRef == "" {
					continue
				}
				if _, exists := seen[serviceRef]; exists {
					continue
				}
				seen[serviceRef] = struct{}{}
				serviceRefsByPrimary[primaryID] = append(serviceRefsByPrimary[primaryID], serviceRef)
				serviceRefSet[serviceRef] = struct{}{}
			}
		}
	}

	servicesByIdentifier, err := loadServicesByReferences(ctx, serviceRefSet)
	if err != nil {
		return nil, err
	}
	for primaryID, serviceRefs := range serviceRefsByPrimary {
		seenServices := make(map[string]struct{})
		for _, serviceRef := range serviceRefs {
			for _, service := range servicesByIdentifier[serviceRef] {
				if _, exists := seenServices[service.PrimaryIdentifier]; exists {
					continue
				}
				seenServices[service.PrimaryIdentifier] = struct{}{}
				serviceForResponse := *service
				serviceForResponse.OtherIdentifiers = nil
				result[primaryID] = append(result[primaryID], &serviceForResponse)
			}
		}
		sort.Slice(result[primaryID], func(i, j int) bool {
			return result[primaryID][i].PrimaryIdentifier < result[primaryID][j].PrimaryIdentifier
		})
	}
	return result, nil
}

func loadServicesByReferences(ctx context.Context, refs map[string]struct{}) (map[string][]*ctdf.Service, error) {
	identifiers := make([]string, 0, len(refs))
	for identifier := range refs {
		identifiers = append(identifiers, identifier)
	}
	servicesByID := make(map[string][]*ctdf.Service, len(identifiers))
	for _, id := range identifiers {
		servicesByID[id] = []*ctdf.Service{}
	}
	if len(identifiers) == 0 {
		return servicesByID, nil
	}

	servicesCollection := database.GetCollection("services")
	serviceOpts := options.Find().SetProjection(bson.D{
		{Key: "_id", Value: 0},
		{Key: "creationdatetime", Value: 0},
		{Key: "modificationdatetime", Value: 0},
		{Key: "routes", Value: 0},
		{Key: "stopnameoverrides", Value: 0},
	})
	seenServices := make(map[string]struct{})
	for start := 0; start < len(identifiers); start += stopServicesLookupChunk {
		end := min(start+stopServicesLookupChunk, len(identifiers))
		chunk := identifiers[start:end]
		cursor, err := servicesCollection.Find(ctx, bson.M{
			"$or": bson.A{
				bson.M{"primaryidentifier": bson.M{"$in": chunk}},
				bson.M{"otheridentifiers": bson.M{"$in": chunk}},
			},
		}, serviceOpts)
		if err != nil {
			return nil, fmt.Errorf("query services by stop references: %w", err)
		}
		for cursor.Next(ctx) {
			var service ctdf.Service
			if err := cursor.Decode(&service); err != nil {
				cursor.Close(ctx)
				return nil, fmt.Errorf("decode service for stop references: %w", err)
			}
			if _, exists := seenServices[service.PrimaryIdentifier]; exists {
				continue
			}
			seenServices[service.PrimaryIdentifier] = struct{}{}
			servicesByID[service.PrimaryIdentifier] = append(servicesByID[service.PrimaryIdentifier], &service)
			for _, identifier := range service.OtherIdentifiers {
				servicesByID[identifier] = append(servicesByID[identifier], &service)
			}
		}
		if err := cursor.Err(); err != nil {
			cursor.Close(ctx)
			return nil, fmt.Errorf("iterate services for stop references: %w", err)
		}
		if err := cursor.Close(ctx); err != nil {
			return nil, fmt.Errorf("close services cursor: %w", err)
		}
	}
	return servicesByID, nil
}
