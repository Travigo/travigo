# Realtime Journeys Mongo Read Migration

This tracks the current read-side state for realtime journeys as reads move behind `pkg/realtime/realtimestore` and Redis.

Already centralised in `realtimestore`:

- `GET /realtime_journeys`
  - Uses `realtimestore.FindActiveWithinBounds`.
  - Still depends on Mongo `vehiclelocation.coordinates` for discovery, then overlays Redis location fields.

- `GET /realtime_journeys/:identifier`
  - Uses `realtimestore.FindByIdentifier`.
  - Reads full realtime journeys from Redis first, then falls back to Mongo by `primaryidentifier`.

- `GET /journeys/:identifier` current realtime journey lookup
  - Uses `realtimestore.FindCurrentForJourney`.
  - Reads the Redis `travigo-journeyid` mapping first, then falls back to Mongo by `journey.primaryidentifier`.

- Local departure board realtime journey lookup
  - Uses `realtimestore.FindCurrentForJourneyIDs`.
  - Reads Redis mappings per journey first, then falls back to a Mongo batch lookup for misses.
  - Block-offset estimation still uses `realtimestore.FindCurrentByJourneyRefs`, which is Mongo-only.

- Vehicle tracker previous-state lookup
  - `pkg/realtime/vehicletracker/realtimejourney.go` now uses `realtimestore.FindByIdentifier`.
  - It no longer reads `realtime_journeys` directly.

- National Rail realtime journey lookups
  - NROD activation uses `realtimestore.FindByIdentifier`.
  - NROD movement and reinstatement use `realtimestore.FindByMapping("TrainID", ...)`.
  - Darwin status/schedule updates use `realtimestore.FindByIdentifier`.
  - Darwin formation/loading/retry paths use `realtimestore.FindByMapping("nationalrailrid", ...)`.

- Realtime journey stats aggregation
  - `pkg/stats/calculator/realtimejourneys.go` delegates to `realtimestore.GetRealtimeJourneys`.

- Redis overlays
  - `vehiclelocation`, `vehiclebearing`, and `vehiclelocationdescription` are applied in `realtimestore`.

## Remaining Direct Mongo Reads

- `pkg/dataaggregator/source/tfl/departureboard.go`
  - Direct `Find` by `stops.<stopID>.timetype` for TfL departure board generation.

- `pkg/dataaggregator/source/tfl/journey.go`
  - Direct `FindOne` by `primaryidentifier`, returning the static journey attached to a TfL realtime journey.

- `pkg/realtime/tflarrivals/modearrivaltracker.go`
  - Direct `FindOne` by realtime journey `primaryidentifier` before updating TfL arrivals.
  - The updated realtime journey is also saved to Redis, but this previous-state read still comes from Mongo.

## Central Mongo Fallbacks In `realtimestore`

These are no longer scattered read paths, but they still read Mongo:

- `FindByIdentifier`
  - Fallback `FindOne` by `primaryidentifier`.

- `FindCurrentForJourney`
  - Fallback `FindOne` by `journey.primaryidentifier` and active `modificationdatetime`.

- `FindCurrentForJourneyIDs`
  - Redis-first loop per journey ID, then fallback batch `Find` by `journey.primaryidentifier`.

- `FindCurrentByJourneyRefs`
  - Mongo-only `FindOne` by `journeyref`.

- `FindActiveWithinBounds`
  - Mongo-only geospatial `Find` by `vehiclelocation.coordinates`.
  - Redis-only realtime journeys are not discoverable by this map/bounds query unless a Mongo document still exists with a matching `vehiclelocation`.

- `GetRealtimeJourneys`
  - Mongo aggregation used by stats.

## Watchers

- `pkg/dbwatch/realtimejourneys.go`
  - Watches `realtime_journeys` with a Mongo change stream.
  - Redis-only realtime journey producers will not trigger these events unless equivalent event publishing is added elsewhere.

## Notes

- `pkg/realtime/realtimestore/writer.go` stores full realtime journeys in Redis and writes lookup mappings for `travigo-journeyid` plus `RealtimeJourney.OtherIdentifiers`.
- `FindByMapping` currently depends on the Redis mapping existing; it does not fall back to Mongo by arbitrary identifier fields.
- `pkg/realtime/vehicletracker/consumer.go` still contains a legacy realtime journey `BulkWrite` block, but the current vehicle tracker trip/location-only paths no longer append realtime journey write models.
- `pkg/realtime/nationalrail/railutils/queue.go` still contains a legacy Mongo bulk writer, but there are currently no `BatchProcessingQueue.Add` callers for realtime journey updates.
