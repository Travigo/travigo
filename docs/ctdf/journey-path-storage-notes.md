# Ordered journey path storage

These are the original sizing notes. The [implementation design and rehearsal](journey-path-storage-design.md) cover the direct CTDF format, separate geometry, API compatibility, and downtime rebuild.

## Why investigate

CTDF stores journey paths as ordered legs. An intermediate stop generally appears as the destination of one leg and the origin of the next. A stored list of ordered stop calls could keep each visit once, with both arrival and departure data on the call. Leg-specific metadata could stay on the outgoing call.

This could reduce the MongoDB working set and the amount of data the tracker decodes. In one production sample, the broad SIRI matching fallback returned about 60,000 candidate documents. In a GTFS-RT burst, the tracker made about 835 scheduled journey reads per second at roughly 16 KB of BSON per read. The experiment below measured size. The effect on latency and throughput is open.

## Measured size

The experiment used the same 12,598 journeys from a public TransLink bus timetable in three freshly imported collections in an isolated MongoDB 8.0 container. The sample contained 356,515 legs and 369,113 stop calls.

| Format | Average journey BSON | Total BSON | Compressed collection allocation | Allocation reduction |
|---|---:|---:|---:|---:|
| Current origin/destination legs | 9.6 KiB | 118.1 MiB | 15.4 MiB | — |
| Ordered stop calls with descriptive fields | 4.3 KiB | 53.0 MiB | 9.1 MiB | 40% |
| Ordered stop calls with compact encoding | 1.8 KiB | 21.6 MiB | 5.8 MiB | 62% |

The plain format stored a stop reference, arrival, departure, and pickup and set-down activities. It stored platforms and leg metadata where present. The compact format also used short field names, integer seconds from timetable midnight where representable, and booleans for common activities. These were sizing prototypes. The sample contained mostly ordinary bus calls, and some savings came from omitting empty fields in the baseline representation. Measure a baseline with empty fields omitted as a control to isolate the effect of changing the path shape.

The production `journeys` collection had 11,926,025 documents when checked on 13 November 2026. It reported 176.6 GiB of logical BSON, 32.5 GiB of allocated collection storage, and 15.2 GiB of indexes. Applying the sample ratios to the allocated collection storage gives **roughly 13 GiB saved for plain calls or 20 GiB for compact calls**, about 28% or 42% of the current combined data and index footprint. These are scenarios based on one bus feed. Rail, long paths, inline geometry, and MongoDB compression and allocation differences could materially change the result. Disk reclaimed from an existing collection also depends on how it is rewritten.

## Where to start

1. Prototype the plain stop-call representation at the storage boundary. Keep the existing CTDF `Journey` and leg-facing consumers usable while measuring. The current model is in `pkg/ctdf/journey.go`. The tracker read is in `pkg/realtime/vehicletracker/journeycache.go`. SIRI’s stop matching is in `pkg/realtime/vehicletracker/identifiers/siri_stops.go`.
2. Verify round trips with representative bus and rail schedules, including repeat visits to the same stop, intermediate and terminal calls, pickup and set-down restrictions, differing platforms or times across adjacent legs, overnight times, and leg-specific tracks, distances, displays, and CIF passing references. Check legacy, amended, and synthetic journeys wherever they enter the same read paths.
3. Compare cold and warm MongoDB reads, BSON bytes, decoding CPU, and matching results for the tracker and SIRI. Include busy-stop departure-board queries and verify the same results, including looping routes and pickup restrictions.
4. Measure index sizes separately. The two existing origin/destination stop indexes were about 5 GiB each. Moving the same stop values to another array does not by itself remove the per-journey multikey entries. Preserve equivalent board lookup coverage during the experiment. A separate stop-call lookup collection would need its own comparison of storage, import cost, and query performance.
5. If the prototype pays off, work out how existing documents and imported data will coexist during rollout, how downstream readers will switch, and how the change can be rolled back.

The sizing script and generated data are currently in `/tmp/travigo-ordered-path.yVXXbB`. Its input was `/tmp/travigo-stop-index-trial.Ptd2OJ/journeys.ndjson`, sampled every fifth record. These are temporary local files and may disappear. No production records or application code were changed for the estimate.
