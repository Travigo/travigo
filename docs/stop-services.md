# Stop-services materialized view

The stop-services builder maintains a materialized view of static timetable
services by stop. Journey publishers record durable source changes, while a
separate command builds the view outside the journey publication path. Service
endpoints and stop indexing always read this materialized view; they require an
initial full backfill and do not fall back to journey scans. A failed build
before manifest publication leaves the last published view available and keeps
the source change eligible for retry.

The view covers static timetable datasets. It intentionally excludes
`gb-networkrail-vstp` and `gb-nationalrail-darwin-amendment`.

## Rollout

Upgrade every static journey publisher first so live timetable publishes
update the durable dirty marker. Run the initial full backfill with the
build-capable binary before deploying the always-on API and stop-indexer
readers. After the backfill succeeds, deploy the batch runner with
`refresh-stop-services` after journey publication and deploy the new readers.

Run one full backfill against the production database before deploying the
always-on readers:

```sh
travigo data-linker stop-services build
```

The command builds all eligible static dataset contributions and publishes
their manifest only after the source revision is still current. It uses
bounded batches and returns an error if source writes overlap the build, so a
failed initial backfill can be retried safely.
Production build duration and resource use have not yet been benchmarked; use
the first backfill to measure them. The default client write batch is 1,000
rows; use `--batch-size` on a manual full or per-dataset build to adjust it.

The existing importer batch run refreshes pending datasets immediately after
`link-journeys` publishes them. The task ID is `refresh-stop-services`; include
it in any batch run that selects a custom task subset. The default scheduled
batch trigger includes all plan tasks. The refresh has a 90-minute command
timeout and processes dirty datasets one at a time, using the builder's
1,000-row default write batch. Before the one-time backfill, this task logs a
skip and succeeds; `pending` does not initialize the materialized view itself.
The default batch plan runs `index-stops` after this task, and that indexer
requires the materialized view. Hold the scheduled batch plan until the full
backfill succeeds, or omit `index-stops` from any pre-backfill custom run.

After the full backfill, API service endpoints and `indexer stops` use the
materialized view automatically.

## Dataset updates and removals

Incremental journey publication marks the affected dataset dirty before it
changes the live journey collection. The background worker replaces that
dataset's contribution, including replacing it with an empty contribution when
the dataset no longer has journeys. Other datasets remain in the manifest.
Full backfills rebuild the complete set of eligible static datasets.

Publisher notifications are small durable revision updates; they do not run
the stop-services aggregation inline. A build captures the source revisions
before reading, then checks them again before publishing. If a writer overlaps
the read, or if any captured dataset changes, the build keeps the previous
manifest and leaves the change pending for a later retry. This avoids
publishing a view assembled from a mixed journey snapshot without holding a
long lock against publishers.

Each build writes a new generation before updating the manifest. Previous
generations remain available for at least 24 hours so readers that already
loaded the old manifest can finish. A later successful build prunes retired
generations after that grace period; cleanup may be delayed until the next
successful build if no further timetable changes arrive.

## Failure and recovery

If the refresh task fails, its Kubernetes Job retries up to the batch runner's
configured backoff limit. If a failure occurs before the manifest swap, the
previous contribution stays published and the dirty marker remains pending for
a later batch run, so serving may lag the latest timetable import. Cleanup or
lease release can also fail after the new contribution is already published; check
the build logs and refresh state before treating every reported error as a stale
view. Check the batch-run details and task logs for the dataset and build error.
Service endpoints and stop indexing require the initial full build; they do not
fall back to journey scans if the materialized view has not been initialized.

For a manual retry of pending dataset updates:

```sh
travigo data-linker stop-services pending
```

If a process was forcibly terminated while publishing, its durable active
writer marker can block builds. Stop every journey publisher first, then clear
the abandoned marker and retry the pending refresh:

```sh
travigo data-linker stop-services recover-writers --confirm-no-publishers
travigo data-linker stop-services pending
```

The recovery command increments the affected revisions so those datasets are
rebuild candidates. Do not run it while any journey publisher is active; the
confirmation flag requires an explicit acknowledgement of this precondition.
If recovery reports that source state changed during the operation, recheck that
all publishers are stopped before retrying it.

To rebuild one dataset explicitly:

```sh
travigo data-linker stop-services build --dataset <dataset-id>
```

To rebuild every eligible static dataset:

```sh
travigo data-linker stop-services build
```
