package routes

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func loadStopsByReferences(refs map[string]struct{}, projection bson.M) map[string]*ctdf.Stop {
	result := make(map[string]*ctdf.Stop, len(refs))
	for ref := range refs {
		result[ref] = nil
	}
	if len(refs) == 0 {
		return result
	}
	ids := departureBoardReferenceIdentifiers(refs)
	opts := options.Find()
	if projection != nil {
		opts.SetProjection(projection)
	}
	cursor, err := database.GetCollection("stops").Find(context.Background(), bson.M{"$or": bson.A{bson.M{"primaryidentifier": bson.M{"$in": ids}}, bson.M{"otheridentifiers": bson.M{"$in": ids}}, bson.M{"platforms.primaryidentifier": bson.M{"$in": ids}}}}, opts)
	if err != nil {
		log.Error().Err(err).Msg("Failed to batch load response stops")
		return result
	}
	defer cursor.Close(context.Background())
	// Match StopQuery precedence: primary ID, then alias, then platform.
	priorities := map[string]int{}
	for cursor.Next(context.Background()) {
		var stop ctdf.Stop
		if err := cursor.Decode(&stop); err != nil {
			log.Error().Err(err).Msg("Failed to decode response stop")
			continue
		}
		attach := func(ref string, priority int) {
			if ref != "" && priority > priorities[ref] {
				result[ref] = &stop
				priorities[ref] = priority
			}
		}
		attach(stop.PrimaryIdentifier, 3)
		for _, ref := range stop.OtherIdentifiers {
			attach(ref, 2)
		}
		for _, p := range stop.Platforms {
			if p != nil {
				attach(p.PrimaryIdentifier, 1)
			}
		}
	}
	if err := cursor.Err(); err != nil {
		log.Error().Err(err).Msg("Failed to read response stops")
	}
	return result
}
func hydratePlannerReferences(plans *ctdf.JourneyPlanResults) {
	stops := map[string]*ctdf.Stop{}
	// Only exact endpoint identifiers are authoritative without a lookup.
	// An endpoint alias/platform can be another stop's primary identifier.
	endpoints := map[string]*ctdf.Stop{}
	for _, stop := range []*ctdf.Stop{&plans.OriginStop, &plans.DestinationStop} {
		if stop.PrimaryIdentifier != "" {
			endpoints[stop.PrimaryIdentifier] = stop
			stops[stop.PrimaryIdentifier] = stop
		}
	}
	stopRefs, serviceRefs, operatorRefs := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	for i := range plans.JourneyPlans {
		for k := range plans.JourneyPlans[i].RouteItems {
			item := &plans.JourneyPlans[i].RouteItems[k]
			for _, ref := range []string{item.OriginStopRef, item.DestinationStopRef} {
				if ref != "" && stops[ref] == nil {
					stopRefs[ref] = struct{}{}
				}
			}
			if j := item.Journey; j != nil {
				if j.Service == nil && j.ServiceRef != "" {
					serviceRefs[j.ServiceRef] = struct{}{}
				}
				if j.Operator == nil && j.OperatorRef != "" {
					operatorRefs[j.OperatorRef] = struct{}{}
				}
			}
		}
	}
	for ref, stop := range loadStopsByReferences(stopRefs, nil) {
		if stops[ref] == nil {
			if stop != nil && endpoints[stop.PrimaryIdentifier] != nil {
				// Reuse an endpoint only after resolving the reference with the same
				// primary > alias > platform precedence as every other stop lookup.
				stops[ref] = endpoints[stop.PrimaryIdentifier]
			} else {
				stops[ref] = stop
			}
		}
	}
	services := loadServicesByReferencesWithProjection(serviceRefs, nil)
	operators := loadOperatorsByReferences(operatorRefs)
	for i := range plans.JourneyPlans {
		for k := range plans.JourneyPlans[i].RouteItems {
			item := &plans.JourneyPlans[i].RouteItems[k]
			item.OriginStop = stops[item.OriginStopRef]
			item.DestinationStop = stops[item.DestinationStopRef]
			if j := item.Journey; j != nil {
				if j.Service == nil {
					j.Service = services[j.ServiceRef]
				}
				if j.Operator == nil {
					j.Operator = operators[j.OperatorRef]
				}
			}
		}
	}
}
func hydrateBoardServices(board []*ctdf.DepartureBoard) {
	refs := map[string]struct{}{}
	for _, item := range board {
		if item != nil && item.Journey != nil && item.Journey.Service == nil && item.Journey.ServiceRef != "" {
			refs[item.Journey.ServiceRef] = struct{}{}
		}
	}
	services := loadServicesByReferences(refs)
	for _, item := range board {
		if item != nil && item.Journey != nil && item.Journey.Service == nil {
			item.Journey.Service = services[item.Journey.ServiceRef]
		}
	}
}
