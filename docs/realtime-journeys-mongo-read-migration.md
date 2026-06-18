# Realtime Journeys Mongo Read Migration

This tracks the remaining direct reads from the `realtime_journeys` Mongo collection that still need moving behind `pkg/realtime/realtimestore`.

Already centralised in `realtimestore`:

- `GET /realtime_journeys`
- `GET /realtime_journeys/:identifier`
- `GET /journeys/:identifier` current realtime journey lookup

## Departure Boards

- `pkg/ctdf/departureboard.go`
  - Batched `Find` by `journey.primaryidentifier` and active `modificationdatetime`.
  - Block-based `FindOne` used for offset estimation.

- `pkg/dataaggregator/source/tfl/departureboard.go`
  - `Find` by `stops.<stopID>.timetype` for TfL departure board generation.

- `pkg/dataaggregator/source/tfl/journey.go`
  - `FindOne` by `primaryidentifier`, returning the static journey attached to a TfL realtime journey.

## Realtime Writers That Read Existing State

- `pkg/realtime/vehicletracker/realtimejourney.go`
  - `FindOne` by realtime journey `primaryidentifier` before building the next update.

- `pkg/realtime/tflarrivals/modearrivaltracker.go`
  - `FindOne` by realtime journey `primaryidentifier` before updating TfL arrivals.

- `pkg/realtime/nationalrail/nrod/activation.go`
  - `FindOne` by realtime journey `primaryidentifier` during train activation.

- `pkg/realtime/nationalrail/nrod/movement.go`
  - `FindOne` by `otheridentifiers.TrainID` during TRUST movement processing.

- `pkg/realtime/nationalrail/nrod/reinstatement.go`
  - `FindOne` by `otheridentifiers.TrainID` during TRUST reinstatement processing.

- `pkg/realtime/nationalrail/darwin/pushport.go`
  - `FindOne` by realtime journey `primaryidentifier` during train status processing.
  - `FindOne` by realtime journey `primaryidentifier` during schedule processing.
  - `FindOne` by `otheridentifiers.nationalrailrid` for formation updates.
  - `FindOne` by `otheridentifiers.nationalrailrid` for loading/occupancy updates.

- `pkg/realtime/nationalrail/darwin/retryrecords.go`
  - `FindOne` by `otheridentifiers.nationalrailrid` when retrying delayed formation records.

## Stats And Watchers

- `pkg/stats/calculator/realtimejourneys.go`
  - Aggregates active realtime journeys for stats.

- `pkg/dbwatch/realtimejourneys.go`
  - Watches `realtime_journeys` with a Mongo change stream.
  - This is not a normal read path, but it still depends on Mongo receiving realtime updates.

## Notes

- `pkg/realtime/realtimestore/reader.go` still reads Mongo by design. That is currently the central reader layer, not a remaining direct-read migration target.
- `pkg/realtime/nationalrail/railutils/queue.go` and `pkg/realtime/vehicletracker/consumer.go` get the collection for bulk writes only, so they are not listed above.
