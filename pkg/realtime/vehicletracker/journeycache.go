package vehicletracker

import (
	"context"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

const trackedJourneyCacheMaxEntries = 1000

type cachedTrackedJourney struct {
	Journey             *ctdf.Journey
	PathByOriginStopRef map[string]*ctdf.JourneyPathItem
	LastUsed            time.Time
}

func (consumer *BatchConsumer) getCachedTrackedJourney(journeyID string, now time.Time) (*cachedTrackedJourney, error) {
	consumer.journeyCacheMu.RLock()
	cachedJourney := consumer.journeyCache[journeyID]
	consumer.journeyCacheMu.RUnlock()

	if cachedJourney != nil {
		consumer.journeyCacheMu.Lock()
		cachedJourney.LastUsed = now
		consumer.journeyCacheMu.Unlock()

		return cachedJourney, nil
	}

	journey, err := loadTrackedJourney(journeyID)
	if err != nil {
		return nil, err
	}

	if err := hydrateDestinationStops(journey); err != nil {
		log.Warn().Err(err).Str("journey", journeyID).Msg("Failed to hydrate journey destination stops")
	}
	journey.GetTracks()

	journey.GetService()

	cachedJourney = &cachedTrackedJourney{
		Journey:             journey,
		PathByOriginStopRef: buildPathByOriginStopRef(journey.Path),
		LastUsed:            now,
	}

	consumer.journeyCacheMu.Lock()
	if consumer.journeyCache == nil {
		consumer.journeyCache = map[string]*cachedTrackedJourney{}
	}
	consumer.journeyCache[journeyID] = cachedJourney
	consumer.pruneJourneyCacheLocked()
	consumer.journeyCacheMu.Unlock()

	return cachedJourney, nil
}

func loadTrackedJourney(journeyID string) (*ctdf.Journey, error) {
	var journey *ctdf.Journey
	journeysCollection := database.GetCollection("journeys")
	err := journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": journeyID}).Decode(&journey)

	return journey, err
}

func hydrateDestinationStops(journey *ctdf.Journey) error {
	if journey == nil || len(journey.Path) == 0 {
		return nil
	}

	stopRefs := make([]string, 0, len(journey.Path))
	seenStopRefs := map[string]bool{}
	for _, pathItem := range journey.Path {
		if pathItem.DestinationStop != nil || pathItem.DestinationStopRef == "" || seenStopRefs[pathItem.DestinationStopRef] {
			continue
		}

		seenStopRefs[pathItem.DestinationStopRef] = true
		stopRefs = append(stopRefs, pathItem.DestinationStopRef)
	}

	if len(stopRefs) == 0 {
		return nil
	}

	stopsCollection := database.GetCollection("stops")
	cursor, err := stopsCollection.Find(context.Background(), bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": bson.M{"$in": stopRefs}},
			bson.M{"otheridentifiers": bson.M{"$in": stopRefs}},
		},
	})
	if err != nil {
		return err
	}
	defer cursor.Close(context.Background())

	stopsByIdentifier := map[string]*ctdf.Stop{}
	for cursor.Next(context.Background()) {
		var stop ctdf.Stop
		if err := cursor.Decode(&stop); err != nil {
			return err
		}

		stopCopy := stop
		stopsByIdentifier[stopCopy.PrimaryIdentifier] = &stopCopy
		for _, otherIdentifier := range stopCopy.OtherIdentifiers {
			stopsByIdentifier[otherIdentifier] = &stopCopy
		}
	}
	if err := cursor.Err(); err != nil {
		return err
	}

	for _, pathItem := range journey.Path {
		if pathItem.DestinationStop == nil {
			pathItem.DestinationStop = stopsByIdentifier[pathItem.DestinationStopRef]
		}
	}

	return nil
}

func buildPathByOriginStopRef(path []*ctdf.JourneyPathItem) map[string]*ctdf.JourneyPathItem {
	pathByOriginStopRef := make(map[string]*ctdf.JourneyPathItem, len(path))
	for _, pathItem := range path {
		if pathItem.OriginStopRef != "" {
			pathByOriginStopRef[pathItem.OriginStopRef] = pathItem
		}
	}

	return pathByOriginStopRef
}

func (consumer *BatchConsumer) loadLocation(name string) *time.Location {
	if name == "" {
		return time.Local
	}

	consumer.locationCacheMu.RLock()
	location := consumer.locationCache[name]
	consumer.locationCacheMu.RUnlock()
	if location != nil {
		return location
	}

	loadedLocation, err := time.LoadLocation(name)
	if err != nil {
		log.Warn().Err(err).Str("location", name).Msg("Failed to load location")
		return time.Local
	}

	consumer.locationCacheMu.Lock()
	if consumer.locationCache == nil {
		consumer.locationCache = map[string]*time.Location{}
	}
	consumer.locationCache[name] = loadedLocation
	consumer.locationCacheMu.Unlock()

	return loadedLocation
}

func (consumer *BatchConsumer) pruneJourneyCacheLocked() {
	if len(consumer.journeyCache) <= trackedJourneyCacheMaxEntries {
		return
	}

	type cacheEntry struct {
		journeyID string
		lastUsed  time.Time
	}

	cacheEntries := make([]cacheEntry, 0, len(consumer.journeyCache))
	for journeyID, cachedJourney := range consumer.journeyCache {
		cacheEntries = append(cacheEntries, cacheEntry{
			journeyID: journeyID,
			lastUsed:  cachedJourney.LastUsed,
		})
	}

	sort.Slice(cacheEntries, func(i int, j int) bool {
		return cacheEntries[i].lastUsed.Before(cacheEntries[j].lastUsed)
	})

	excessEntries := len(consumer.journeyCache) - trackedJourneyCacheMaxEntries
	for index := 0; index < excessEntries; index += 1 {
		delete(consumer.journeyCache, cacheEntries[index].journeyID)
	}
}
