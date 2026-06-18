# Realtime Journeys Mongo Write Migration

This tracks the remaining writes to the `realtime_journeys` Mongo collection that would need migrating or throttling as realtime state moves into `pkg/realtime/realtimestore` and Redis.

Already moved out of Mongo:

- `pkg/realtime/realtimestore/writer.go`
  - `UpdateLocationDescription` writes vehicle location descriptions to Redis.

## Actual Mongo Write Sinks

- `pkg/realtime/vehicletracker/consumer.go`
  - `BulkWrite` of vehicle tracker realtime journey update models.
  - This is the main sink for `pkg/realtime/vehicletracker/realtimejourney.go` and `pkg/realtime/vehicletracker/locationonly.go`.

- `pkg/realtime/tflarrivals/modearrivaltracker.go`
  - `BulkWrite` of TfL arrival realtime journey update models.
  - `DeleteMany` for stale TfL realtime journeys that were not updated in the current run.

- `pkg/realtime/nationalrail/railutils/queue.go`
  - Shared `BulkWrite` queue for National Rail realtime journey update models.
  - This is the main sink for NROD and Darwin realtime journey updates.

## Vehicle Tracker Write Producers

- `pkg/realtime/vehicletracker/realtimejourney.go`
  - Builds an upsert `UpdateOneModel` by realtime journey `primaryidentifier`.
  - Updates volatile vehicle fields such as `vehiclelocation`, `vehiclebearing`, stop progress, occupancy, offset, stop estimates, and datasource timestamp.
  - Also performs a direct `DeleteOne` if a corrupt realtime journey document exists without a journey.

- `pkg/realtime/vehicletracker/locationonly.go`
  - Builds a non-upsert `UpdateOneModel` by realtime journey `primaryidentifier`.
  - Updates only `modificationdatetime`, `vehiclelocation`, and `vehiclebearing`.

## TfL Arrival Write Producers

- `pkg/realtime/tflarrivals/modearrivaltracker.go`
  - Builds upsert `UpdateOneModel`s for TfL realtime journeys.
  - Updates journey payload, stops, departed/next stop refs, datasource timestamp, and related realtime fields.
  - Writes vehicle location descriptions through Redis via `realtimestore.UpdateLocationDescription`.
  - Deletes stale realtime journeys for the same TfL datasource run using `DeleteMany`.

## National Rail NROD Write Producers

- `pkg/realtime/nationalrail/nrod/activation.go`
  - Builds upsert `UpdateOneModel`s for train activation.
  - Creates or refreshes realtime journeys, then queues them through `railutils.BatchProcessingQueue`.

- `pkg/realtime/nationalrail/nrod/movement.go`
  - Builds upsert `UpdateOneModel`s for TRUST movement events.
  - Updates departed/next stop refs, stop timestamps, active state, timeout, and Redis location descriptions.
  - Queues writes through `railutils.BatchProcessingQueue`.

- `pkg/realtime/nationalrail/nrod/reinstatement.go`
  - Builds upsert `UpdateOneModel`s for reinstatement.
  - Sets `activelytracked`, clears cancellation, updates modification time, then queues through `railutils.BatchProcessingQueue`.

## National Rail Darwin Write Producers

- `pkg/realtime/nationalrail/darwin/pushport.go`
  - Builds upsert `UpdateOneModel`s for train status processing.
  - Builds upsert `UpdateOneModel`s for schedule processing.
  - Builds upsert `UpdateOneModel`s for formation updates by `otheridentifiers.nationalrailrid`.
  - Builds upsert `UpdateOneModel`s for loading/occupancy updates by `otheridentifiers.nationalrailrid`.
  - All of these are queued through `railutils.BatchProcessingQueue`.

## Notes

- `pkg/realtime/realtimestore/reader.go` still reads Mongo by design; it is not a write path.
- `pkg/database/collections.go` creates indexes on `realtime_journeys`; it is not a realtime data write path.
- Service alert, journey, retry record, and stats writes are not included here because they do not write to `realtime_journeys`.
- Highest-volume Mongo write candidates are likely vehicle tracker bulk writes and TfL arrival bulk writes/deletes.
