# Realtime Journeys Mongo Read Migration

This tracks the current read-side state for realtime journeys now that normal realtime journey reads have moved behind `pkg/realtime/realtimestore` and Redis.

## Redis-backed Reads

- `GET /realtime_journeys/:identifier`
  - Uses `realtimestore.FindByIdentifier`.
  - Reads the full realtime journey document from Redis.

- `GET /journeys/:identifier` current realtime journey lookup
  - Uses `realtimestore.FindCurrentForJourney`.
  - Reads the Redis `travigo-journeyid` mapping, then loads the mapped realtime journey from Redis.

- Local departure board realtime journey lookup
  - Uses `realtimestore.FindCurrentForJourneyIDs`.
  - Reads Redis mappings per static journey ID.
  - Block-offset estimation uses `realtimestore.FindCurrentByJourneyRefs`, which now also resolves through the static journey ID mappings.

- TfL departure board realtime lookup
  - `pkg/dataaggregator/source/tfl/departureboard.go` uses `realtimestore.FindTFLDepartureBoardJourneys`.
  - Redis sorted sets keyed by `realtime-journeys:tfl-stop:<stopID>` replace the old Mongo `stops.<stopID>.timetype` query.
  - The reader loads full realtime journeys from Redis after finding matching journey IDs in the stop index.

- TfL realtime journey lookup
  - `pkg/dataaggregator/source/tfl/journey.go` uses `realtimestore.FindByIdentifier`.
  - `pkg/realtime/tflarrivals/modearrivaltracker.go` uses `realtimestore.FindByIdentifier` before updating a TfL realtime journey.

- Vehicle tracker previous-state lookup
  - `pkg/realtime/vehicletracker/realtimejourney.go` uses `realtimestore.FindByIdentifier`.

- National Rail realtime journey lookups
  - NROD activation uses `realtimestore.FindByIdentifier`.
  - NROD movement and reinstatement use `realtimestore.FindByMapping("TrainID", ...)`.
  - Darwin status/schedule updates use `realtimestore.FindByIdentifier`.
  - Darwin formation/loading/retry paths use `realtimestore.FindByMapping("nationalrailrid", ...)`.

- Redis overlays
  - `vehiclelocation`, `vehiclebearing`, and `vehiclelocationdescription` are applied in `realtimestore`.

## Remaining Direct Mongo Reads

No normal request, dataaggregator, or realtime producer path currently performs an active Mongo `Find`, `FindOne`, or `Aggregate` against `realtime_journeys`.

## Not Yet Rebuilt In Redis

- `GET /realtime_journeys`
  - Still calls `realtimestore.FindActiveWithinBounds`.
  - `FindActiveWithinBounds` currently returns `errors.ErrUnsupported`; the old Mongo bounds query is commented out.
  - The map/live-location list still needs a Redis geospatial or location-index reader.

- Realtime journey stats
  - `pkg/stats/calculator/realtimejourneys.go` delegates to `realtimestore.GetRealtimeJourneys`.
  - `realtimestore.GetRealtimeJourneys` currently returns empty counters; the old Mongo aggregation is commented out with `TODO MOVE TO REDIS`.

## Watchers And Events

- `pkg/realtime/realtimestore.SaveRealtimeJourney`
  - Compares the previous Redis realtime journey document with the new one.
  - Publishes created, cancellation, platform set, and platform changed events to `events-queue` after a successful save.
- The old realtime journey Mongo change-stream watcher has been removed from `pkg/dbwatch`; dbwatch still handles service alert Mongo events.

## Redis Indexes And Keys

- Full realtime journey document:
  - `realtime-journey:<primaryIdentifier>/details`

- Identifier mappings:
  - `realtime-journey-mapping:travigo-journeyid:<journeyID>`
  - `realtime-journey-mapping:<otherIdentifierName>:<otherIdentifierValue>`

- TfL departure board stop index:
  - `realtime-journeys:tfl-stop:<stopID>`
  - Sorted-set score is the stop arrival Unix timestamp.

## Notes

- `FindByIdentifier` no longer has a Mongo fallback.
- `FindByMapping` depends on the Redis mapping existing; it does not fall back to Mongo by arbitrary identifier fields.
- `pkg/realtime/realtimestore/filters.go` still contains Mongo-style filter helpers, but current `realtimestore` readers do not use them for active `realtime_journeys` reads.
