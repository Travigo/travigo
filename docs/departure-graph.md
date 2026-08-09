# Rolling departure graph

The departure graph runs as a separate internal service using
`travigo departure-graph run`. Web API replicas make HTTP requests to it for
scheduled departure candidates. Journey planning uses the same source because
its vehicle expansion already reads departure boards. Arrival boards and
filtered board queries continue to use the existing MongoDB/Redis candidate
path.

The graph is deliberately non-blocking:

- Snapshot restore runs before the graph service starts listening. Web API
  requests fall back to the existing scheduled-journey path while it is
  unavailable.
- A request for an incomplete stop/date performs the existing indexed MongoDB
  lookup, adds those journeys to the graph, and marks only that stop complete.
- Adding a journey indexes all of its origin stops, but propagated stops remain
  incomplete until directly filled or a full scan finishes.
- One background cursor slowly scans the journeys collection and constructs the
  configured rolling service-date window.
- A partial generation restored after a restart remains available while its
  background scan fills the missing graph. A later refresh replaces a complete
  generation in place, so the process never retains two complete graphs.
- The active generation is periodically checkpointed as a versioned,
  zstd-compressed snapshot using a temporary file and atomic rename. A final
  checkpoint is attempted during graceful shutdown.

Journey paths are stored once even when a journey operates on several days.
Service-date membership and per-stop departure indexes contain integer journey
references. CTDF journey and path objects are materialised only for board
candidates returned to a request. Realtime remains outside the graph and is
applied by the shared board generator using journey and stop-occurrence identity.

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
| `TRAVIGO_DEPARTURE_GRAPH_SNAPSHOT_INTERVAL` | `15m` | Interval between restart checkpoints. |

The web API Helm chart keeps the API as its existing stateless Deployment and
adds a separate, single-replica graph StatefulSet plus an internal headless
Service. The graph pod has a restart-stable `ReadWriteOnce` claim, avoiding
concurrent snapshot writers and duplicate background MongoDB scans. It requests
6 GiB of memory by default; this should be adjusted using the emitted
stored-journey, path, and bucket counts after the first production build.

The current format is departures-only. An arrival index can later be added as a
second integer index over the same compact journey/path records without changing
the lazy fill, rolling generation, or persistence model.
