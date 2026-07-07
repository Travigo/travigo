# Realtime Journeys Mongo Write Migration

This tracks the current write-side state for `realtime_journeys` now that realtime journey state is stored in Redis through `pkg/realtime/realtimestore`.

## Redis Write Helpers

- `pkg/realtime/realtimestore/writer.go`
  - `SaveRealtimeJourney` writes the full realtime journey JSON to Redis.
  - `SaveRealtimeJourney` writes lookup mappings for `travigo-journeyid` and every entry in `RealtimeJourney.OtherIdentifiers`.
  - `SaveRealtimeJourney` compares the previous Redis document with the new one and publishes created, cancellation, platform set, and platform changed events to `events-queue`.
  - Mapping/detail TTL is based on `RealtimeJourney.TimeoutDurationMinutes`.
  - `IndexTFLDepartureBoardJourney` writes TfL departure board stop indexes to Redis sorted sets.
  - `UpdateLocationDescription` writes vehicle location descriptions to Redis.
  - `UpdateLocation` writes live vehicle location and bearing to Redis.

## Redis-backed Write Producers

- `pkg/realtime/vehicletracker/realtimejourney.go`
  - Updates the in-memory `ctdf.RealtimeJourney`.
  - Writes live `vehiclelocation` and `vehiclebearing` to Redis.
  - Saves the full realtime journey to Redis with `realtimestore.SaveRealtimeJourney`.

- `pkg/realtime/vehicletracker/locationonly.go`
  - Writes live `vehiclelocation` and `vehiclebearing` to Redis only.

- `pkg/realtime/tflarrivals/modearrivaltracker.go`
  - Updates TfL realtime journeys in memory.
  - Writes vehicle location descriptions with `realtimestore.UpdateLocationDescription`.
  - Saves full realtime journeys with `realtimestore.SaveRealtimeJourney`.
  - Updates the TfL departure board Redis stop index with `realtimestore.IndexTFLDepartureBoardJourney`.

- `pkg/realtime/nationalrail/nrod/activation.go`
  - Creates or refreshes realtime journeys, then saves them to Redis.

- `pkg/realtime/nationalrail/nrod/movement.go`
  - Updates train movement state, writes Redis location descriptions, then saves full realtime journeys to Redis.

- `pkg/realtime/nationalrail/nrod/reinstatement.go`
  - Updates reinstatement state and saves realtime journeys to Redis.
  - Still deletes a service alert from Mongo, but does not write `realtime_journeys`.

- `pkg/realtime/nationalrail/darwin/pushport.go`
  - Train status, schedule, formation, and loading updates save realtime journeys to Redis.
  - Service alerts, retry records, and datadump records may still use Mongo, but those are not `realtime_journeys` writes.

- `pkg/realtime/nationalrail/darwin/retryrecords.go`
  - Finds matching realtime journeys through Redis mappings, then re-processes formation records through the Darwin Redis write path.

## Remaining Actual Mongo Writes

No active code path currently writes to the `realtime_journeys` Mongo collection.

## Not Yet Rebuilt In Redis

- TfL stale journey cleanup
  - `pkg/realtime/tflarrivals/modearrivaltracker.go` has the old Mongo `DeleteMany` cleanup commented out.
  - Redis detail keys expire by realtime journey timeout, and TfL stop indexes are trimmed opportunistically by score, but the old per-dataset “delete everything not seen in this run” behavior has not been rebuilt.

- Map/live-location index
  - Live locations are written to Redis, but the map/list endpoint still needs a Redis reader/index to replace the old Mongo geospatial query.

- Realtime journey stats
  - Stats are not currently rebuilt from Redis; `realtimestore.GetRealtimeJourneys` returns empty counters.

## Event Publishing

- Realtime journey events publish from `realtimestore.SaveRealtimeJourney`.
- The old `pkg/dbwatch/realtimejourneys.go` Mongo change-stream watcher has been removed.

## Notes

- Service alert, retry record, datadump, journey, and importer writes may still use Mongo, but they do not write `realtime_journeys`.
- `pkg/realtime/vehicletracker/consumer.go` now bulk-writes service alerts only.
- The old National Rail realtime journey bulk queue file is no longer present.
