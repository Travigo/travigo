# Realtime Journeys Mongo Write Migration

This tracks the current write-side state for `realtime_journeys` as realtime state moves into `pkg/realtime/realtimestore` and Redis.

## Redis Write Helpers

- `pkg/realtime/realtimestore/writer.go`
  - `SaveRealtimeJourney` writes the full realtime journey JSON to Redis.
  - `SaveRealtimeJourney` also writes Redis lookup mappings for `travigo-journeyid` and every entry in `RealtimeJourney.OtherIdentifiers`.
  - `UpdateLocationDescription` writes vehicle location descriptions to Redis.
  - `UpdateLocation` writes live vehicle location and bearing to Redis.

## Already Moved Out Of Mongo

- `pkg/realtime/vehicletracker/realtimejourney.go`
  - Updates the in-memory `ctdf.RealtimeJourney`.
  - Writes live `vehiclelocation` and `vehiclebearing` to Redis.
  - Saves the full realtime journey to Redis with `realtimestore.SaveRealtimeJourney`.
  - No longer returns a Mongo realtime journey write model.

- `pkg/realtime/vehicletracker/locationonly.go`
  - Writes live `vehiclelocation` and `vehiclebearing` to Redis only.
  - No longer updates `modificationdatetime` in Mongo.

- `pkg/realtime/nationalrail/nrod/activation.go`
  - Creates or refreshes the realtime journey, then saves it to Redis.
  - No longer queues a Mongo realtime journey write.

- `pkg/realtime/nationalrail/nrod/movement.go`
  - Updates train movement state in the realtime journey, writes Redis location descriptions, then saves the full journey to Redis.
  - No longer queues a Mongo realtime journey write.

- `pkg/realtime/nationalrail/nrod/reinstatement.go`
  - Updates reinstatement state and saves the realtime journey to Redis.
  - Still deletes a service alert from Mongo, but does not write `realtime_journeys`.

- `pkg/realtime/nationalrail/darwin/pushport.go`
  - Train status, schedule, formation, and loading updates save realtime journeys to Redis.
  - Service alerts, retry records, and datadump records may still use Mongo, but those are not `realtime_journeys` writes.

- `pkg/realtime/nationalrail/darwin/retryrecords.go`
  - Finds matching realtime journeys through Redis mappings, then re-processes formation records through the Darwin Redis write path.

## Remaining Actual Mongo Writes

- `pkg/realtime/tflarrivals/modearrivaltracker.go`
  - Still performs `BulkWrite` upserts to `realtime_journeys`.
  - Still performs `DeleteMany` for stale TfL realtime journeys not updated in the current run.
  - Also saves each updated realtime journey to Redis with `realtimestore.SaveRealtimeJourney`.
  - Also writes vehicle location descriptions to Redis with `realtimestore.UpdateLocationDescription`.

## Legacy Mongo Write Plumbing Still Present

- `pkg/realtime/vehicletracker/consumer.go`
  - Still declares `realtimeJourneyOperations` and contains a realtime journey `BulkWrite` block.
  - The current trip and location-only update methods no longer return write models, so this block should not receive vehicle tracker realtime journey writes in normal flow.
  - The service alert `BulkWrite` in the same file is still active, but it writes `service_alerts`, not `realtime_journeys`.

- `pkg/realtime/nationalrail/railutils/queue.go`
  - Still contains a `BulkWrite` worker for `realtime_journeys`.
  - There are currently no `BatchProcessingQueue.Add` callers for realtime journey updates, so this appears to be unused legacy plumbing.

## Watcher Impact

- `pkg/dbwatch/realtimejourneys.go`
  - Depends on Mongo change streams from `realtime_journeys`.
  - Realtime journey producers that now write Redis only will not trigger these Mongo change-stream events.

## Notes

- `pkg/realtime/realtimestore/reader.go` still reads Mongo for fallback and map/stat paths; it is not a write path.
- `pkg/database/collections.go` creates indexes on `realtime_journeys`; it is not a realtime data write path.
- The highest-volume remaining actual Mongo realtime journey write path is now TfL arrivals.
- The map live-location query and stats still depend on Mongo state, even where the main realtime journey document has moved to Redis.
