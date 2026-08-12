# Rolling journey graph

The journey graph runs as a separate internal service using
`travigo departure-graph run`. It owns the complete in-memory scheduled network:
canonical stop nodes and aliases, spatial origin cells, walking/change edges,
journey path edges, service-day membership and per-stop departures.

`POST /v1/plans` performs the complete time-dependent label-setting search in
that process. Web API sends stop identifiers or origin coordinates and search
constraints directly to it. The response contains compact journey references
and timed transfer legs; Web API only batch-hydrates those journey references,
attaches realtime/reference data, and formats the public response. It does not
expand departure boards, query transfers or calculate routes. `POST
/v1/departures` continues to serve the departure board from the same graph.

Before expanding any departure times, the graph performs a bounded reverse
search over its static transit topology. Compact reverse-transfer and
stop-to-arriving-journey indexes identify the stops that can reach the requested
destination within the remaining vehicle-leg limit. The time-dependent search
only expands states inside that corridor. This is a safe superset rather than a
fixed guessed path, so timetable-valid alternatives are not discarded. Up to
eight single-destination corridors are cached on the active graph generation.

The graph is deliberately non-blocking:

- Snapshot restore runs before the graph service starts listening. The readiness
  endpoint stays unavailable until both timetable and topology are ready;
  journey planning has no MongoDB calculation fallback.
- A request for an incomplete stop/date performs the existing indexed MongoDB
  lookup, adds those journeys to the graph, and marks only that stop complete.
- Adding a journey indexes all of its origin stops, but propagated stops remain
  incomplete until directly filled or a full scan finishes.
- One background cursor slowly scans the journeys collection and constructs the
  configured rolling service-date window.
- A partial generation restored after a restart remains available while its
  background scan resumes after the last checkpointed MongoDB `_id`. A later
  refresh replaces a complete generation in place, so the process never retains
  two complete graphs.
- The active generation is periodically checkpointed as a versioned,
  zstd-compressed snapshot using a temporary file and atomic rename. A final
  checkpoint is attempted during graceful shutdown.

Journey paths are stored once even when a journey operates on several days.
Service-date membership and the departure index contain integer journey
references. Transfer adjacency uses compact fixed-width records and offsets;
stop aliases are canonicalised to graph node numbers. Coordinate origins use a
small in-memory spatial grid. Planning is a forward earliest-arrival search over
these scheduled and transfer edges with dominance by stop and vehicle-leg count.
The old reverse arrival hint index is no longer retained, freeing its
multi-million map buckets for the actual topology.

Path records contain only the fields used by departure boards and planner
searches. Destination platforms are omitted because graph-backed arrival boards
are not supported, and each origin arrival is reconstructed from the preceding
leg's destination arrival; only the first arrival is retained on the journey.
This keeps each path record at 28 bytes. After a rolling scan completes, the
graph also releases its all-string, journey-identity and completed-day build
maps, retaining a smaller stop-reference lookup for serving requests. Those
build maps are reconstructed only if the sealed generation later needs a lazy
fill. Rolling rebuilds start with a fresh generation so completed-day index
entries cannot be duplicated as the configured date window shifts.

## Configuration

| Environment variable | Default | Meaning |
| --- | --- | --- |
| `TRAVIGO_DEPARTURE_GRAPH_ADDRESS` | empty | Web API URL for the internal graph service; empty disables graph requests. |
| `TRAVIGO_DEPARTURE_GRAPH_BACKGROUND_ENABLED` | `true` | Enables rolling full scans. |
| `TRAVIGO_DEPARTURE_GRAPH_SNAPSHOT_PATH` | empty | Snapshot file; persistence is disabled when empty. |
| `TRAVIGO_DEPARTURE_GRAPH_DAYS_BEHIND` | `1` | Service days retained before today. |
| `TRAVIGO_DEPARTURE_GRAPH_DAYS_AHEAD` | `1` | Service days retained after today. |
| `TRAVIGO_DEPARTURE_GRAPH_BATCH_SIZE` | `1000` | Journeys processed between pauses. |
| `TRAVIGO_DEPARTURE_GRAPH_BATCH_PAUSE` | `250ms` | Background throttle pause. |
| `TRAVIGO_DEPARTURE_GRAPH_INITIAL_BUILD_DELAY` | `30s` | Delay before an uncached initial build. |
| `TRAVIGO_DEPARTURE_GRAPH_REFRESH_INTERVAL` | `24h` | Delay between completed full rebuilds. |
| `TRAVIGO_DEPARTURE_GRAPH_RETRY_INTERVAL` | `1m` | Delay before resuming a failed background scan. |
| `TRAVIGO_DEPARTURE_GRAPH_SNAPSHOT_INTERVAL` | `15m` | Interval between restart checkpoints. |

The web API Helm chart keeps the API as its existing stateless Deployment and
adds a separate, single-replica graph StatefulSet plus an internal headless
Service. The graph pod has a restart-stable `ReadWriteOnce` claim, avoiding
concurrent snapshot writers and duplicate background MongoDB scans. It requests
6 GiB of memory by default; this should be adjusted using the emitted
stored-journey, path, and bucket counts after the first production build.
The pod runs as UID/GID 1000 and mounts the claim with `fsGroup: 1000`, allowing
atomic snapshot creation on a newly provisioned volume.

## Operational statistics

`GET /v1/stats` returns a live JSON snapshot covering:

- `Requests`: departure-request totals, failures and in-flight requests; the
  completed request rate and latency over the last 60 seconds; and lifetime
  average, maximum and most recent latency.
- The existing top-level `Strings`, `Journeys`, `Paths`, `DepartureBuckets`,
  `CompleteStops` and `CompleteDays` fields remain unchanged. `Stops`,
  `StopIdentifiers`, `TransferEdges`, `StaticRideLinks`, `TopologyReady` and
  `StaticRoutingReady` report planner topology; `ArrivalBuckets` remains zero
  for compatibility. `Lookups` adds hit
  and miss counts and hit rate plus lazy-fill counts, failures, in-flight fills
  and average/maximum fill time.
- `BackgroundBuild`: whether a scan is active, estimated and scanned
  journey counts, count restored from a resumable checkpoint, progress from `0`
  to `1`, scan rate, estimated time remaining, duration, active journey-day
  count and successful/failed build history.
- `Snapshot`: active write state, successful/failed write counts, last
  write and restore duration, compressed file size and latest errors.
- `Memory`: Go heap, stack, runtime system allocation, heap objects, goroutines,
  garbage collections and latest GC pause. On Linux it also reports process RSS
  and cgroup usage/limit when those kernel files are available.

The endpoint calculates metrics from counters and current runtime state; it does
not traverse the graph. `GET /healthz` remains the lightweight probe endpoint.
