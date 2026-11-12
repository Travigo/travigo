# Stop-services materialized view

The stop-services builder maintains a materialized view of static timetable
services by stop. Journey publishers record durable source changes, while a
separate command builds the view outside the journey publication path. Service
endpoints and stop indexing always read this materialized view; they require an
initial full backfill for complete coverage and do not fall back to journey
scans. While a full backfill is publishing dataset generations, readers use
the generations already available and omit datasets that are not built yet.
A failed build before manifest publication leaves the last published view
available and keeps the source change eligible for retry.

The view covers static timetable datasets. It intentionally excludes
`gb-networkrail-vstp` and `gb-nationalrail-darwin-amendment`.

## Rollout

Upgrade every static journey publisher first so live timetable publishes
update the durable dirty marker. Run the importer batch plan with
`refresh-stop-services` after journey publication. That task builds the full
view on its first run and refreshes dirty datasets on later runs. The always-on
API serves contributions already built during the initial backfill. Stop
indexing waits until the complete view is current.

The same task can be run manually against the production database before
deploying the always-on readers:

```sh
travigo data-linker stop-services pending
```

When the view is uninitialized, this command builds all eligible static dataset
contributions and publishes their manifest only after the source revision is
still current. It uses bounded batches and returns an error if source writes
overlap the build, so a failed initial backfill can be retried safely. Once
initialized, it refreshes only pending datasets.
Production build duration and resource use have not yet been benchmarked; use
the first backfill to measure them. The default client write batch is 1,000
rows; use `--batch-size` on a manual full or per-dataset build to adjust it.

The importer batch run builds or refreshes the view immediately after
`link-journeys` publishes. The task ID is `refresh-stop-services`; include it
in any batch run that selects a custom task subset. The default scheduled batch
trigger includes all plan tasks. It has a 90-minute command timeout and uses the
builder's 1,000-row default write batch. The default batch plan runs
`index-stops` after this task.

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
