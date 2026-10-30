# Performance Review implementation sets

Prepared 26 October 2026. Covers all **156 reviewed comments in 63 Go files**, with exactly one owning set and a stable `PR001`–`PR156` ID per comment.

Start with **A–F** for the best expected benefit relative to implementation effort. **G–I** establish board correctness, reliable publication and freshness needed by the larger optimisations. **J–Q** are the larger follow-on packages. Within those constraints, the order favours likely savings over architectural scope.

The payoff estimates come from the reviewed call paths, not production measurements or promised speedup percentages. Reorder after measuring the dominant workload. Existing `[P1]` and `[P2]` markers describe finding severity; a later letter does not make a data-loss, race or stale-data defect less urgent. In particular, individual fixes in G, H, I and L can be expedited independently of the performance sequence.

Each set is a work package that can be handed off separately, not necessarily one commit. `S` means a local change with focused verification; `M` spans a component or call path; `L` coordinates several components; `XL` needs a protocol, storage or lifecycle design and usually several PRs. Effort includes verification and migration work, not just changed lines.

| Set | Work package | Expected payoff | Effort | Prerequisites | Findings |
| --- | --- | --- | --- | --- | ---: |
| [A](#set-a) | Realtime calculation and cache quick wins | High potential CPU/allocation savings on vehicle updates; bounded queue-handle memory | S-M | None; capture a baseline first | 7 |
| [B](#set-b) | Diagnostic costs and useful measurements | High when verbose diagnostics or Elasticsearch backpressure dominate | S | None; can accompany A and C | 5 |
| [C](#set-c) | API response enrichment and projections | Fewer MongoDB round trips and decoded bytes on board/journey/planner requests | S-M | None; preserve existing response views | 5 |
| [D](#set-d) | HTTP, archive and temporary-file lifecycle | Less repeated I/O; fewer stalled workers, leaked resources and invalid cached responses | M | None; coordinate shared files with C and F | 10 |
| [E](#set-e) | Bounded request work and cancellation | High protection against expensive requests and avoidable work after cancellation | M | None; agree request contracts before rollout | 5 |
| [F](#set-f) | Reuse batch-import conversion work | High for TransXChange repetition and large GTFS imports | M | None; reuse D helpers when touching file I/O | 7 |
| [G](#set-g) | Departure-board and stop-occurrence correctness | Correct boards plus reduced accidental block-query overfetch; prerequisite for K | M-L | E request validation helps; otherwise can proceed independently | 13 |
| [H](#set-h) | Reliable import completion and generation publication | Avoid failed refreshes, destructive cleanup and repeated full reimports; foundation for later gains | L | D validation/resource lifecycle; F shared-profile safety before more parallel conversion | 12 |
| [I](#set-i) | Versioned caches and graph freshness | Prevent persistent stale results and replace repeated broad invalidation; unlocks J/K/N/P | L | H published-generation contract | 7 |
| [J](#set-j) | Shared static lookups and route matching | High reduction in cold-start reads, repeated matching and duplicated process memory | L | A occurrence helpers; E budgets; G occurrence rules; I generation/invalidation contract | 16 |
| [K](#set-k) | Departure-board query and candidate pipeline | High at busy stops and under simultaneous polling | L | C enrichment; E budgets; G board semantics; I trustworthy graph freshness | 11 |
| [L](#set-l) | Realtime ordering, failures and safe retries | High correctness/recovery value; required before M throughput increases | XL | D/E bounded dependency calls; coordinate VSTP publication with I | 16 |
| [M](#set-m) | Bounded ingestion and queue throughput | High during backlog and slow dependencies once retry/ordering guarantees hold | L | L commit/retry semantics; D/E time and size budgets | 10 |
| [N](#set-n) | Search index build and publication | Faster rebuilds and reliable search snapshots | L | D checked Elasticsearch responses; H/I generation and dependency tracking | 9 |
| [O](#set-o) | Bounded importer memory and write volume | High on national or dense feeds; lower peak memory and redundant database writes | XL | D resource ownership; F immutable templates; H validated publication | 13 |
| [P](#set-p) | Graph memory, locking and planner reuse | Lower long-run memory and maintenance-related tail latency; less repeated corridor work | XL | E request budgets; I correct generations/remapping; coordinate graph.go edits with K | 5 |
| [Q](#set-q) | Compact runtime payloads and notification work | Potentially high at large route/subscription volumes; requires compatibility migrations | XL | L committed immutable event state; I/J static versions and cache contracts | 5 |

## How to run the sets

A, B and C can be separate early jobs. D, E and F can also progress without waiting for the later architecture work, but D/F share importer files and C/D/E share API files. H and L can be designed independently; H → I enables J/K/N/P, while L must precede M. O needs the bounded completion/publication contracts from D/F/H. Q needs committed event semantics from L and versioned static data from I/J. Agree file ownership or use separate branches where sets touch the same file.

Before each implementation, capture a representative baseline and preserve a result reference. For realtime work measure processing time, event age, commands/writes, allocations and retained memory. For boards/planning measure correctness, p95 latency, rows examined and payload size. For imports/indexes measure duration, peak memory, descriptor/temp-file use and confirmed writes. Only keep an optimisation when the result contract still holds and the relevant cost improves; correctness fixes must pass their failure/replay scenarios even if their normal-path timing is unchanged.

Find all comments for a package with `rg -n -F "Performance Review [Set A]" pkg`, replacing A as needed. Stable IDs remain useful after lines move. The original review text is retained after the labels.

Suggested handoff: “Implement Set A from docs/performance-review-plan.md and the Performance Review [Set A] comments. Respect its prerequisites, preserve the documented behaviour, and verify its completion checks. Keep unrelated work intact. Do not stage or commit changes unless asked.”

## Set details and complete finding inventory

<a id="set-a"></a>

### Set A — Realtime calculation and cache quick wins

**Expected payoff:** High potential CPU/allocation savings on vehicle updates; bounded queue-handle memory. **Effort:** S-M. **Prerequisites:** None; capture a baseline first.

Reuse the events queue handle, populate the successful TfL stop cache, compile recurring expressions and cache validated time zones, replace sort-on-eviction, and avoid rebuilding unchanged predictions. Index stop occurrences and legacy stop lookups using the existing journey lifetime; process-wide cache sharing belongs to J.

These are local changes to repeated work, mostly without a storage or API migration. Queue-handle retention and linear scans have direct code evidence; the workload determines the size of the gain.

**Completion checks:** Compare allocations and CPU per update on short, long and looping routes, including partial stop maps and unchanged offsets. Verify next-stop transitions, missing estimates and timetable changes still rebuild predictions. Sustained event publication must retain a bounded number of queue handles; cache eviction must remain bounded.

| Status | ID | Finding | Affected comment |
| --- | --- | --- | ---- | 
| Patched | PR121 | Reuse events queue handles | [realtime/realtimestore/events.go:49](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/realtimestore/events.go:49) |
| | PR123 | Avoid repeated fallback scans in delay summaries | [realtime/realtimestore/events.go:236](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/realtimestore/events.go:236) |
| | PR134 | Reuse TfL expressions and validated time zones | [realtime/tflarrivals/modearrivaltracker.go:276](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/tflarrivals/modearrivaltracker.go:276) |
| | PR137 | Populate the successful TfL primary-stop cache | [realtime/tflarrivals/modearrivaltracker.go:636](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/tflarrivals/modearrivaltracker.go:636) |
| | PR150 | Replace sorting-based cache eviction | [realtime/vehicletracker/journeycache.go:169](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/journeycache.go:169) |
||  PR153 | Decide whether predictions need rebuilding before allocation | [realtime/vehicletracker/realtimejourney.go:114](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/realtimejourney.go:114) |
| Patched | PR154 | Resolve stop-update occurrences without repeated path scans | [realtime/vehicletracker/realtimejourney.go:129](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/realtimejourney.go:129) |

<a id="set-b"></a>

### Set B — Diagnostic costs and useful measurements

**Expected payoff:** High when verbose diagnostics or Elasticsearch backpressure dominate. **Effort:** S. **Prerequisites:** None; can accompany A and C.

Skip disabled telemetry before encoding, sample/deduplicate routine diagnostics, summarise per-call logging, and bound diagnostic writes separately from business updates. Add a small set of stage timings, cache and queue counters, event age and consumer progress measurements.

Removes frequent optional database/logging work and makes subsequent prioritisation measurable. The benefit is smaller where those diagnostics are already disabled or cheap.

**Completion checks:** Replay representative feeds with diagnostics enabled, disabled and deliberately slow. Compare diagnostic bytes/writes, processing latency and event age. Verify business updates are unchanged and dropped diagnostic samples are counted without unbounded labels or buffers.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR033 | Bound repeated GTFS diagnostic writes | [dataimporter/formats/gtfs/realtime.go:277](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/gtfs/realtime.go:277) |
| PR103 | Summarise Darwin per-call diagnostics | [realtime/nationalrail/darwin/pushport.go:277](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/darwin/pushport.go:277) |
| PR116 | Bound VSTP diagnostic writes | [realtime/nationalrail/nrod/stomp.go:147](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/nrod/stomp.go:147) |
| PR144 | Bound and sample tracker identification telemetry | [realtime/vehicletracker/consumer.go:452](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/consumer.go:452) |
| PR156 | Measure tracker stages, progress and event age | [realtime/vehicletracker/stats.go:32](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/stats.go:32) |

<a id="set-c"></a>

### Set C — API response enrichment and projections

**Expected payoff:** Fewer MongoDB round trips and decoded bytes on board/journey/planner requests. **Effort:** S-M. **Prerequisites:** None; preserve existing response views.

Batch unique stop/service/operator references across returned results, keep request-local negative entries, project only required display fields, and skip unused outer journey enrichment for realtime_only. Resolve arrival and cancelled-terminal display references after selection where possible.

Repeated reference reads are visible in the request path, and the changes can preserve existing public response schemas.

**Completion checks:** Compare queries and decoded bytes per board/plan, including repeated destinations and absent references. Compare all supported response views, aliases, platform display names, service overrides and nested realtime journey tracks.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR001 | Skip unused outer enrichment for realtime_only | [api/routes/journeys.go:45](/Users/aaronclaydon/projects/travigo/travigo/pkg/api/routes/journeys.go:45) |
| PR002 | Batch plan reference hydration | [api/routes/planner.go:175](/Users/aaronclaydon/projects/travigo/travigo/pkg/api/routes/planner.go:175) |
| PR006 | Batch missing board display lookups | [api/routes/stops.go:399](/Users/aaronclaydon/projects/travigo/travigo/pkg/api/routes/stops.go:399) |
| PR007 | Project board service display fields | [api/routes/stops.go:639](/Users/aaronclaydon/projects/travigo/travigo/pkg/api/routes/stops.go:639) |
| PR009 | Defer and batch arrival/cancelled-origin hydration | [ctdf/departureboard.go:236](/Users/aaronclaydon/projects/travigo/travigo/pkg/ctdf/departureboard.go:236) |

<a id="set-d"></a>

### Set D — HTTP, archive and temporary-file lifecycle

**Expected payoff:** Less repeated I/O; fewer stalled workers, leaked resources and invalid cached responses. **Effort:** M. **Prerequisites:** None; coordinate shared files with C and F.

Close and validate HTTP responses, make retry backoff concurrency-safe, check extraction copies, close/remove temporary artifacts on every path, hash checked download bytes once, and process bundle members one at a time. Add bounded download/body policies and keep TfL last-good data on failed polls.

Resource ownership and validation are contained fixes. Shared download helpers can serve batch importers and TfL without changing feed semantics.

**Completion checks:** Exercise non-2xx responses, malformed bodies, stalled reads, oversized downloads, bad ZIP checksums and failed extraction. Check file descriptors, temporary disk use and connection reuse over repeated polls. Run concurrent retry cases under the race detector; failed TfL fetches must not publish a successful empty snapshot.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR008 | Validate and close stop-search responses | [api/routes/stops.go:751](/Users/aaronclaydon/projects/travigo/travigo/pkg/api/routes/stops.go:751) |
| PR036 | Own and validate all GTFS extraction artifacts | [dataimporter/formats/gtfs/schedule.go:160](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/gtfs/schedule.go:160) |
| PR064 | Hash checked downloads without a full reread | [dataimporter/manager/manager.go:210](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/manager/manager.go:210) |
| PR065 | Open bundle members one at a time | [dataimporter/manager/manager.go:256](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/manager/manager.go:256) |
| PR067 | Bound download duration and size | [dataimporter/manager/manager.go:485](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/manager/manager.go:485) |
| PR068 | Close both download and parsing descriptors | [dataimporter/manager/manager.go:518](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/manager/manager.go:518) |
| PR087 | Make Elasticsearch retry backoff concurrency-safe | [elastic_client/connection.go:33](/Users/aaronclaydon/projects/travigo/travigo/pkg/elastic_client/connection.go:33) |
| PR088 | Validate and close Elasticsearch startup responses | [elastic_client/connection.go:60](/Users/aaronclaydon/projects/travigo/travigo/pkg/elastic_client/connection.go:60) |
| PR095 | Validate and close index management responses | [indexer/stops.go:82](/Users/aaronclaydon/projects/travigo/travigo/pkg/indexer/stops.go:82) |
| PR132 | Validate TfL fetches and retain last-good arrivals | [realtime/tflarrivals/modearrivaltracker.go:90](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/tflarrivals/modearrivaltracker.go:90) |

<a id="set-e"></a>

### Set E — Bounded request work and cancellation

**Expected payoff:** High protection against expensive requests and avoidable work after cancellation. **Effort:** M. **Prerequisites:** None; agree request contracts before rollout.

Validate finite board counts, graph date/materialisation limits and planner cost controls. Carry a single request budget through discovery, graph calls, fallback and enrichment. Give coalesced fills a bounded lifetime independent of individual waiters.

This mainly improves tail latency and overload behaviour; normal small requests may not become much faster. Do this before increasing server concurrency.

**Completion checks:** Cover negative/huge counts, distant dates, large transfer radii and search limits. Cancel a request during each stage and check work stops within budget. One cancelled coalesced caller must not fail healthy waiters, and expired waiters must return promptly.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR003 | Validate bounded positive board counts | [api/routes/stops.go:267](/Users/aaronclaydon/projects/travigo/travigo/pkg/api/routes/stops.go:267) |
| PR020 | Carry one deadline through graph and fallback | [dataaggregator/source/localdepartureboard/departureboard.go:185](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataaggregator/source/localdepartureboard/departureboard.go:185) |
| PR071 | Separate fill lifetime from waiter cancellation | [departuregraph/graph.go:445](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/graph.go:445) |
| PR083 | Bound planner cost controls and setup work | [departuregraph/planner.go:404](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/planner.go:404) |
| PR084 | Bound graph HTTP materialisation and date horizons | [departuregraph/server.go:128](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/server.go:128) |

<a id="set-f"></a>

### Set F — Reuse batch-import conversion work

**Expected payoff:** High for TransXChange repetition and large GTFS imports. **Effort:** M. **Prerequisites:** None; reuse D helpers when touching file I/O.

Parse TransXChange profiles, organisation indexes, route/link timings and journey overrides once into immutable templates. Cache GTFS calendars by service, skip unrequested journey/shape work, project existing-journey comparison metadata, and deduplicate combined-track writes within an enrichment run.

Removes work multiplied by departures while also eliminating the shared operating-profile mutation race. Streaming the whole importer is deliberately a later package, O.

**Completion checks:** Compare imported journey IDs, availability, activities, times and geometry references for the same feed. Cover override precedence, duplicate link references, calendar exceptions and frequency clones. Run shared-profile conversion with the race detector; measure CPU, allocations and repeated track upserts.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR039 | Skip unused GTFS journey/geometry stages | [dataimporter/formats/gtfs/schedule.go:458](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/gtfs/schedule.go:458) |
| PR041 | Reuse GTFS service calendar templates | [dataimporter/formats/gtfs/schedule.go:609](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/gtfs/schedule.go:609) |
| PR054 | Make TransXChange profile conversion immutable | [dataimporter/formats/transxchange/operatingprofile.go:33](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/transxchange/operatingprofile.go:33) |
| PR057 | Precompute TransXChange route/link templates | [dataimporter/formats/transxchange/transxchange.go:469](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/transxchange/transxchange.go:469) |
| PR060 | Project and chunk existing TransXChange journey metadata | [dataimporter/formats/transxchange/transxchange.go:855](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/transxchange/transxchange.go:855) |
| PR061 | Index TransXChange journey overrides | [dataimporter/formats/transxchange/vehiclejourney.go:42](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/transxchange/vehiclejourney.go:42) |
| PR101 | Deduplicate combined-track construction/upserts | [journeytracks/enrich.go:401](/Users/aaronclaydon/projects/travigo/travigo/pkg/journeytracks/enrich.go:401) |

<a id="set-g"></a>

### Set G — Departure-board and stop-occurrence correctness

**Expected payoff:** Correct boards plus reduced accidental block-query overfetch; prerequisite for K. **Effort:** M-L. **Prerequisites:** E request validation helps; otherwise can proceed independently.

Preserve operating date and stop occurrence from candidate selection to realtime matching. Fix timezone handling, previous-service-day overflow, duplicate journey/realtime identity, year-zero block bounds, activity/replacement flags, partial-load errors, TfL requested-time handling and stale sorted-set slots. Align TfL prediction indices with the full selected route.

Most of this package protects visible correctness rather than promising a throughput gain. Candidate limiting and fallback suppression must build on these rules.

**Completion checks:** Cover repeated stops, equivalent journeys with only one live update, tomorrow versus today cancellation, 25:30 service times, DST/offset-equivalent inputs, pickup-only arrivals, replacement buses, cold/warm parity and missing Redis details. Dependency errors must remain errors instead of cached empty boards.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR004 | Normalise board timezone and service date | [api/routes/stops.go:296](/Users/aaronclaydon/projects/travigo/travigo/pkg/api/routes/stops.go:296) |
| PR005 | Propagate board source errors | [api/routes/stops.go:327](/Users/aaronclaydon/projects/travigo/travigo/pkg/api/routes/stops.go:327) |
| PR010 | Resolve realtime identity before journey deduplication | [ctdf/departureboard.go:330](/Users/aaronclaydon/projects/travigo/travigo/pkg/ctdf/departureboard.go:330) |
| PR012 | Select the correct repeated-stop occurrence | [ctdf/departureboard.go:385](/Users/aaronclaydon/projects/travigo/travigo/pkg/ctdf/departureboard.go:385) |
| PR013 | Match realtime to the operating service date | [ctdf/departureboard.go:411](/Users/aaronclaydon/projects/travigo/travigo/pkg/ctdf/departureboard.go:411) |
| PR014 | Initialise year-zero block departure maxima correctly | [ctdf/departureboard.go:591](/Users/aaronclaydon/projects/travigo/travigo/pkg/ctdf/departureboard.go:591) |
| PR018 | Include previous service days for overnight calls | [dataaggregator/source/localdepartureboard/departureboard.go:80](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataaggregator/source/localdepartureboard/departureboard.go:80) |
| PR023 | Preserve activity and replacement-bus metadata | [dataaggregator/source/localdepartureboard/departureboard.go:390](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataaggregator/source/localdepartureboard/departureboard.go:390) |
| PR024 | Reject incomplete Mongo timetable loads | [dataaggregator/source/localdepartureboard/departureboard.go:425](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataaggregator/source/localdepartureboard/departureboard.go:425) |
| PR025 | Respect requested time in TfL live boards | [dataaggregator/source/tfl/departureboard.go:100](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataaggregator/source/tfl/departureboard.go:100) |
| PR080 | Match cold/warm board activity filtering | [departuregraph/graph.go:1207](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/graph.go:1207) |
| PR125 | Refill after stale TfL sorted-set entries | [realtime/realtimestore/reader.go:238](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/realtimestore/reader.go:238) |
| PR135 | Assign TfL stop occurrences against the selected full route | [realtime/tflarrivals/modearrivaltracker.go:321](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/tflarrivals/modearrivaltracker.go:321) |

<a id="set-h"></a>

### Set H — Reliable import completion and generation publication

**Expected payoff:** Avoid failed refreshes, destructive cleanup and repeated full reimports; foundation for later gains. **Effort:** L. **Prerequisites:** D validation/resource lifecycle; F shared-profile safety before more parallel conversion.

Propagate parse/write/finalisation errors, drain writers with a real completion signal, validate complete documents and positive bounded frequency expansion, and distinguish absent optional tables from broken input. Introduce dataset ownership and unique fenced generations, reconcile transfers, and give TransXChange tracks collision-safe stable identity.

A successful generation must mean all required data is complete before cleanup or hashes are published. That contract supports cache validity, index checkpoints, idempotent writes and bounded streaming later.

**Completion checks:** Inject truncated XML/CSV/CIF/JSON, short fixed-width rows, invalid frequencies, partial bulk failures and finalisation failures. The last published generation must remain usable. Test overlapping imports, same-second starts, duplicate local track IDs, absent transfers and retrying each failed phase without unbounded duplicate rows.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR029 | Validate CIF record width and scanner completion | [dataimporter/formats/cif/schedule.go:127](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/cif/schedule.go:127) |
| PR031 | Drain and close GTFS writers with true completion | [dataimporter/formats/gtfs/batchimporter.go:92](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/gtfs/batchimporter.go:92) |
| PR038 | Propagate GTFS table import failures | [dataimporter/formats/gtfs/schedule.go:335](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/gtfs/schedule.go:335) |
| PR040 | Reconcile removed GTFS transfer rules | [dataimporter/formats/gtfs/schedule.go:479](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/gtfs/schedule.go:479) |
| PR045 | Fail NaPTAN import on unsuccessful bulk writes | [dataimporter/formats/naptan/naptan.go:126](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/naptan/naptan.go:126) |
| PR047 | Require complete NaPTAN XML | [dataimporter/formats/naptan/xml_parser.go:24](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/naptan/xml_parser.go:24) |
| PR048 | Propagate CORPUS read/decode failures | [dataimporter/formats/networkrail-corpus/parser.go:9](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/networkrail-corpus/parser.go:9) |
| PR055 | Validate positive bounded TransXChange frequency expansion | [dataimporter/formats/transxchange/transxchange.go:317](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/transxchange/transxchange.go:317) |
| PR059 | Avoid TransXChange track ID collisions across files | [dataimporter/formats/transxchange/transxchange.go:772](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/transxchange/transxchange.go:772) |
| PR062 | Require complete TransXChange XML | [dataimporter/formats/transxchange/xml_parser.go:27](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/transxchange/xml_parser.go:27) |
| PR063 | Fence imports with unique owned generations | [dataimporter/manager/manager.go:148](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/manager/manager.go:148) |
| PR066 | Propagate cleanup/version/report publication failures | [dataimporter/manager/manager.go:326](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/manager/manager.go:326) |

<a id="set-i"></a>

### Set I — Versioned caches and graph freshness

**Expected payoff:** Prevent persistent stale results and replace repeated broad invalidation; unlocks J/K/N/P. **Effort:** L. **Prerequisites:** H published-generation contract.

Tie stop-service caches, graph topology, complete-day markers and reusable journey paths to consumed data generations. Remap/prune all journey/day membership structures consistently. Publish VSTP changes before invalidation, prevent old in-flight fills repopulating new cache generations, and replace per-stop repeated keyspace scans with bounded invalidation.

Faster reuse is unsafe while cached data can survive imports indefinitely. This set establishes freshness semantics before reducing fallbacks or sharing more immutable data.

**Completion checks:** Import additions, removals and same-ID path changes for an already-complete day, plus stop aliases/coordinates/transfers. Cover restore, off-window fill/compact/refill, and an old cache fill racing publication. Verify VSTP create/update/delete invalidates old and new affected calls and does not multiply keyspace scans by route length.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR016 | Version stop-to-service cache entries | [dataaggregator/source/databaselookup/servicesbystop.go:20](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataaggregator/source/databaselookup/servicesbystop.go:20) |
| PR072 | Refresh graph stop/transfer topology by generation | [departuregraph/graph.go:562](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/graph.go:562) |
| PR073 | Invalidate complete-day state after timetable changes | [departuregraph/graph.go:574](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/graph.go:574) |
| PR076 | Remap/prune all graph journey-day membership | [departuregraph/graph.go:783](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/graph.go:783) |
| PR077 | Refresh same-ID graph journey paths | [departuregraph/graph.go:1009](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/graph.go:1009) |
| PR111 | Replace repeated per-stop keyspace invalidation scans | [realtime/nationalrail/nrod/cachebust.go:13](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/nrod/cachebust.go:13) |
| PR118 | Invalidate VSTP caches after fenced publication | [realtime/nationalrail/nrod/vstp.go:379](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/nrod/vstp.go:379) |

<a id="set-j"></a>

### Set J — Shared static lookups and route matching

**Expected payoff:** High reduction in cold-start reads, repeated matching and duplicated process memory. **Effort:** L. **Prerequisites:** A occurrence helpers; E budgets; G occurrence rules; I generation/invalidation contract.

Separate lightweight identification/schedule metadata from geometry, share bounded immutable static data by version, and coalesce cold misses. Precompute functional hashes, stop occurrences, TfL route sequences and geometry progress. Use narrow update/timeout metadata reads, prefilter eligible location candidates, and distinguish transient failures from confirmed negative cache results.

The potential saving is large, but shared caches need clear byte budgets, lifetimes and immutable ownership. Keep the per-run mutable state and commit protocol in L.

**Completion checks:** Measure cold and warm reads, decoded bytes, cache churn and retained memory with five consumers and mixed GTFS/SIRI/rail/TfL traffic. Cover loops, aliases, geometry-free trips, invalid zones, cache invalidation and recovery from dependency failures. Preserve remaining-TTL versus configured-duration semantics and avoid increasing worker concurrency here.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR106 | Precompute Darwin occurrence and alias lookup | [realtime/nationalrail/darwin/stop_occurrence.go:9](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/darwin/stop_occurrence.go:9) |
| PR110 | Use narrow consist allocation/update reads | [realtime/nationalrail/linxconsist/processor.go:44](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/linxconsist/processor.go:44) |
| PR113 | Reuse versioned static data for NROD movements | [realtime/nationalrail/nrod/movement.go:65](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/nrod/movement.go:65) |
| PR119 | Batch/cache rail stop identity lookups | [realtime/nationalrail/railutils/stopcache.go:28](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/railutils/stopcache.go:28) |
| PR120 | Do not negative-cache rail dependency outages | [realtime/nationalrail/railutils/stopcache.go:48](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/railutils/stopcache.go:48) |
| PR130 | Read compact expiration policy for location-only updates | [realtime/realtimestore/writer.go:298](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/realtimestore/writer.go:298) |
| PR133 | Use narrow/batched TfL update reads | [realtime/tflarrivals/modearrivaltracker.go:237](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/tflarrivals/modearrivaltracker.go:237) |
| PR136 | Pre-resolve TfL route matching data | [realtime/tflarrivals/modearrivaltracker.go:355](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/tflarrivals/modearrivaltracker.go:355) |
| PR143 | Separate transient identification failures from negative results | [realtime/vehicletracker/consumer.go:326](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/consumer.go:326) |
| PR145 | Cache/index operator service identification | [realtime/vehicletracker/identifiers/sirivm.go:64](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/identifiers/sirivm.go:64) |
| PR146 | Precompute versioned journey identification hashes | [realtime/vehicletracker/identifiers/sirivm.go:211](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/identifiers/sirivm.go:211) |
| PR147 | Project lightweight identification candidates | [realtime/vehicletracker/identifiers/util.go:45](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/identifiers/util.go:45) |
| PR148 | Share bounded immutable journey caches | [realtime/vehicletracker/journeycache.go:14](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/journeycache.go:14) |
| PR149 | Load geometry only when matching needs it | [realtime/vehicletracker/journeycache.go:45](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/journeycache.go:45) |
| PR151 | Precompute geometry progress and reuse matches | [realtime/vehicletracker/journeymatch.go:95](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/journeymatch.go:95) |
| PR152 | Filter location candidates before heavy hydration | [realtime/vehicletracker/locationidentification.go:37](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/locationidentification.go:37) |

<a id="set-k"></a>

### Set K — Departure-board query and candidate pipeline

**Expected payoff:** High at busy stops and under simultaneous polling. **Effort:** L. **Prerequisites:** C enrichment; E budgets; G board semantics; I trustworthy graph freshness.

Suppress duplicate Mongo fallback only when graph completeness/freshness is known, paginate until enough eligible entries survive, merge ordered buckets instead of sorting the whole day, and exchange compact board candidates. Coalesce timetable cache misses, index eligible block predecessors, bound Redis reads, reduce sorted-set membership churn, and validate selective Mongo date queries with explain.

This combines reductions that otherwise risk working against one another: fewer candidates must still preserve late realtime departures, and eliminating fallback requires a current graph.

**Completion checks:** Compare complete board results against an exhaustive reference over busy hubs, cancellations, loops, late arrivals and sparse realtime coverage. Measure p95 latency, rows examined, response bytes and Redis commands with many concurrent polls. Exercise exhausted pages, partial days and Mongo/Redis/graph outages under one request budget.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR011 | Share a bounded board generation work budget | [ctdf/departureboard.go:357](/Users/aaronclaydon/projects/travigo/travigo/pkg/ctdf/departureboard.go:357) |
| PR015 | Index eligible block predecessors by generation/date | [ctdf/departureboard.go:605](/Users/aaronclaydon/projects/travigo/travigo/pkg/ctdf/departureboard.go:605) |
| PR019 | Avoid duplicate scheduled-only board fallback | [dataaggregator/source/localdepartureboard/departureboard.go:102](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataaggregator/source/localdepartureboard/departureboard.go:102) |
| PR021 | Fetch further candidate windows until usable results suffice | [dataaggregator/source/localdepartureboard/departureboard.go:227](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataaggregator/source/localdepartureboard/departureboard.go:227) |
| PR022 | Coalesce timetable cache fills | [dataaggregator/source/localdepartureboard/departureboard.go:368](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataaggregator/source/localdepartureboard/departureboard.go:368) |
| PR078 | Merge ordered departure buckets for top results | [departuregraph/graph.go:1152](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/graph.go:1152) |
| PR079 | Return compact board candidates | [departuregraph/graph.go:1166](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/graph.go:1166) |
| PR081 | Prefilter timetable queries conservatively by service date | [departuregraph/mongo_loader.go:85](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/mongo_loader.go:85) |
| PR082 | Verify availability scan selectivity and indexes | [departuregraph/mongo_loader.go:150](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/mongo_loader.go:150) |
| PR124 | Bound board and block Redis batch reads | [realtime/realtimestore/reader.go:77](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/realtimestore/reader.go:77) |
| PR129 | Update only changed TfL departure memberships | [realtime/realtimestore/writer.go:223](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/realtimestore/writer.go:223) |

<a id="set-l"></a>

### Set L — Realtime ordering, failures and safe retries

**Expected payoff:** High correctness/recovery value; required before M throughput increases. **Effort:** XL. **Prerequisites:** D/E bounded dependency calls; coordinate VSTP publication with I.

Define canonical run/occurrence identity and per-stream source versions, then fence commits across vehicle tracking, Darwin, NROD and TfL. Preserve independent location/prediction timestamps and heartbeat renewal. Propagate persistence/read failures, acknowledge or commit only resolved work, reject partial frames, correct retry-record consumption and alert identity/cleanup, and remove unsafe shared CIF map mutations. Batch frame reads only with immutable previous state for event comparison.

More workers or batched acknowledgements amplify the existing lost-update and retry problems. This is a coordinated protocol package, likely several PRs, rather than a tuning-only change. Its individual P1 loss/race fixes can be prioritised immediately.

**Completion checks:** Race two writers in reversed source-time order and replay each update. Inject failures between mapping, state, location, history and event writes; verify bounded recovery with no stale overwrite, silent acknowledgement or duplicate notification. Cover source corrections, partial frames, Kafka partition gaps, retry expiry, alert withdrawals and invalid HTTP snapshots.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR032 | Use GTFS source versions with separate liveness renewal | [dataimporter/formats/gtfs/realtime.go:96](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/gtfs/realtime.go:96) |
| PR049 | Reject incomplete SIRI-SX frames | [dataimporter/formats/siri_sx/siri_sx.go:48](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/siri_sx/siri_sx.go:48) |
| PR050 | Use stable target-aware SIRI alert identity | [dataimporter/formats/siri_sx/siri_sx.go:144](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/siri_sx/siri_sx.go:144) |
| PR053 | Reject incomplete SIRI-VM frames | [dataimporter/formats/siri_vm/siri_vm.go:193](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/siri_vm/siri_vm.go:193) |
| PR102 | Group rail frame reads while preserving event semantics | [realtime/nationalrail/darwin/pushport.go:41](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/darwin/pushport.go:41) |
| PR104 | Consume exact retry records only after successful writes | [realtime/nationalrail/darwin/retryrecords.go:28](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/darwin/retryrecords.go:28) |
| PR107 | Reject partial Darwin XML/gzip frames | [realtime/nationalrail/darwin/xml_parser.go:18](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/darwin/xml_parser.go:18) |
| PR109 | Do not commit failed consist updates | [realtime/nationalrail/linxconsist/kafka.go:62](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/linxconsist/kafka.go:62) |
| PR112 | Order NROD updates by source event time | [realtime/nationalrail/nrod/movement.go:48](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/nrod/movement.go:48) |
| PR114 | Fence concurrent rail run updates | [realtime/nationalrail/nrod/movement.go:158](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/nrod/movement.go:158) |
| PR117 | Synchronise shared CIF stop caches | [realtime/nationalrail/nrod/vstp.go:362](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/nrod/vstp.go:362) |
| PR127 | Reuse immutable previous state for realtime update reads | [realtime/realtimestore/reader.go:735](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/realtimestore/reader.go:735) |
| PR138 | Avoid serial alert existence reads with safe upserts | [realtime/tflarrivals/modedisruptiontracker.go:99](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/tflarrivals/modedisruptiontracker.go:99) |
| PR139 | Fix alert cleanup only after validated complete snapshots | [realtime/tflarrivals/modedisruptiontracker.go:357](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/tflarrivals/modedisruptiontracker.go:357) |
| PR142 | Fence realtime commits instead of admission alone | [realtime/vehicletracker/consumer.go:262](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/consumer.go:262) |
| PR155 | Propagate tracker persistence/read failures before acknowledgement | [realtime/vehicletracker/realtimejourney.go:246](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/realtimejourney.go:246) |

<a id="set-m"></a>

### Set M — Bounded ingestion and queue throughput

**Expected payoff:** High during backlog and slow dependencies once retry/ordering guarantees hold. **Effort:** L. **Prerequisites:** L commit/retry semantics; D/E time and size budgets.

Batch publication by bytes/count with confirmed outcomes, apply cancellable admission before entire feeds enter the queue, and use bounded work lanes ordered by canonical run. Evaluate acknowledgement and Kafka commit batching against the actual library APIs. Upsert active TfL line/trip pairs instead of inserting every observation.

This can improve throughput substantially but needs backpressure and failure semantics first. Raising consumer counts or prefetch alone is not completion of the set.

**Completion checks:** Sustain feed bursts through slow Redis/Mongo/broker periods while tracking event age, memory, queued bytes and in-flight work. Preserve same-run order and the unresolved prefix of Kafka partitions. Verify partial publish retries, acknowledgement failures, shutdown draining and unique TfL pair retention.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR034 | Batch and confirm GTFS queue publication | [dataimporter/formats/gtfs/realtime.go:333](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/gtfs/realtime.go:333) |
| PR035 | Apply iterative admission control before publishing entire feeds | [dataimporter/formats/gtfs/realtime.go:367](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/gtfs/realtime.go:367) |
| PR051 | Confirm and batch SIRI-SX publication | [dataimporter/formats/siri_sx/siri_sx.go:177](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/siri_sx/siri_sx.go:177) |
| PR052 | Confirm and batch SIRI-VM publication | [dataimporter/formats/siri_vm/siri_vm.go:161](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/siri_vm/siri_vm.go:161) |
| PR105 | Bound Darwin dispatch and acknowledge durable success | [realtime/nationalrail/darwin/stomp.go:68](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/darwin/stomp.go:68) |
| PR108 | Batch Kafka commits without crossing unresolved offsets | [realtime/nationalrail/linxconsist/kafka.go:46](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/linxconsist/kafka.go:46) |
| PR115 | Bound and order NROD/VSTP dispatch | [realtime/nationalrail/nrod/stomp.go:66](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/nationalrail/nrod/stomp.go:66) |
| PR131 | Upsert unique active TfL line/trip pairs | [realtime/tflarrivals/busconsumer.go:192](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/tflarrivals/busconsumer.go:192) |
| PR140 | Bound tracker work per event and canonical run | [realtime/vehicletracker/consumer.go:112](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/consumer.go:112) |
| PR141 | Measure and safely batch acknowledgement work | [realtime/vehicletracker/consumer.go:184](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/vehicletracker/consumer.go:184) |

<a id="set-n"></a>

### Set N — Search index build and publication

**Expected payoff:** Faster rebuilds and reliable search snapshots. **Effort:** L. **Prerequisites:** D checked Elasticsearch responses; H/I generation and dependency tracking.

Produce a stop-to-service relation once per timetable generation, use appropriate stop dataset filters/projections and verify actual indexes. Capture consumed dependency versions, report every bulk/enrichment failure, build versioned indexes and atomically switch aliases after validation. Align identifier-prefix mapping/query and decouple optional board warming from publication.

Avoids repeated per-stop discovery while making completed indexing mean a verified, current serving index. Basic response-body fixes are already in D.

**Completion checks:** Measure build time, rows examined and enrichment reads. Reject partial bulk/enrichment/cursor failures, preserve the current alias, and verify expected document counts and prefix search. Import a changed dependency during a build and confirm it is not skipped on the next run; slow warming must not hold publication hostage.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR017 | Build stop-to-service relations once per generation | [dataaggregator/source/databaselookup/servicesbystop.go:44](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataaggregator/source/databaselookup/servicesbystop.go:44) |
| PR026 | Support stop indexing filters with verified indexes | [database/collections.go:153](/Users/aaronclaydon/projects/travigo/travigo/pkg/database/collections.go:153) |
| PR089 | Report bulk enqueue/flush/item failures | [elastic_client/connection.go:93](/Users/aaronclaydon/projects/travigo/travigo/pkg/elastic_client/connection.go:93) |
| PR092 | Checkpoint captured indexing dependency versions | [indexer/cli.go:47](/Users/aaronclaydon/projects/travigo/travigo/pkg/indexer/cli.go:47) |
| PR093 | Rebuild search when timetable/branding dependencies change | [indexer/stops.go:54](/Users/aaronclaydon/projects/travigo/travigo/pkg/indexer/stops.go:54) |
| PR094 | Publish validated search indexes through serving aliases | [indexer/stops.go:63](/Users/aaronclaydon/projects/travigo/travigo/pkg/indexer/stops.go:63) |
| PR096 | Align identifier-prefix search mapping and query | [indexer/stops.go:131](/Users/aaronclaydon/projects/travigo/travigo/pkg/indexer/stops.go:131) |
| PR097 | Fail incomplete index enrichment/iteration | [indexer/stops.go:299](/Users/aaronclaydon/projects/travigo/travigo/pkg/indexer/stops.go:299) |
| PR098 | Decouple optional board warming from index publication | [indexer/stops.go:309](/Users/aaronclaydon/projects/travigo/travigo/pkg/indexer/stops.go:309) |

<a id="set-o"></a>

### Set O — Bounded importer memory and write volume

**Expected payoff:** High on national or dense feeds; lower peak memory and redundant database writes. **Effort:** XL. **Prerequisites:** D resource ownership; F immutable templates; H validated publication.

Stream/spool large CIF, NaPTAN, GTFS and TransXChange structures through byte/count-bounded workers. Cap external-merge fan-in and frequency expansion, retain only needed shapes, and avoid quadratic endpoint materialisation. Separate content changes from seen-generation markers, make refresh writes idempotent, diff track references and support cleanup filters with measured indexes.

This tackles broad memory and write amplification, but changes ingestion architecture and publication semantics. Keep format-specific migrations as separate PRs under one budget/completion contract.

**Completion checks:** Run full-size and deliberately dense feeds with fixed memory/file-descriptor budgets; measure peak heap, GC, database writes and throughput. Test duplicate keys, overlays, forward area/platform references, frequency boundaries and interruption during cleanup/track replacement. Output must match a validated reference generation and retries must not accumulate duplicates.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR027 | Spool CIF archives and grouped train definitions | [dataimporter/formats/cif/cif.go:59](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/cif/cif.go:59) |
| PR028 | Make CIF refresh writes idempotent | [dataimporter/formats/cif/cif.go:240](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/cif/cif.go:240) |
| PR030 | Bound GTFS writer bytes as well as row count | [dataimporter/formats/gtfs/batchimporter.go:16](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/gtfs/batchimporter.go:16) |
| PR037 | Retain only needed GTFS shapes | [dataimporter/formats/gtfs/schedule.go:293](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/gtfs/schedule.go:293) |
| PR042 | Stream bounded GTFS frequency occurrences | [dataimporter/formats/gtfs/schedule.go:988](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/gtfs/schedule.go:988) |
| PR043 | Cap stop-time merge fan-in | [dataimporter/formats/gtfs/stoptime_sorter.go:49](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/gtfs/stoptime_sorter.go:49) |
| PR044 | Bound NaPTAN conversion workers and buffers | [dataimporter/formats/naptan/naptan.go:77](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/naptan/naptan.go:77) |
| PR046 | Stream/spool NaPTAN while preserving station relationships | [dataimporter/formats/naptan/xml_parser.go:12](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/naptan/xml_parser.go:12) |
| PR056 | Bound TransXChange worker buffers | [dataimporter/formats/transxchange/transxchange.go:365](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/transxchange/transxchange.go:365) |
| PR058 | Separate changed content from seen-generation markers | [dataimporter/formats/transxchange/transxchange.go:728](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/formats/transxchange/transxchange.go:728) |
| PR069 | Verify indexes for dataset cleanup predicates | [dataimporter/manager/manager.go:528](/Users/aaronclaydon/projects/travigo/travigo/pkg/dataimporter/manager/manager.go:528) |
| PR099 | Diff track reference replacements before publishing | [journeytracks/enrich.go:51](/Users/aaronclaydon/projects/travigo/travigo/pkg/journeytracks/enrich.go:51) |
| PR100 | Avoid quadratic route endpoint index materialisation | [journeytracks/enrich.go:306](/Users/aaronclaydon/projects/travigo/travigo/pkg/journeytracks/enrich.go:306) |

<a id="set-p"></a>

### Set P — Graph memory, locking and planner reuse

**Expected payoff:** Lower long-run memory and maintenance-related tail latency; less repeated corridor work. **Effort:** XL. **Prerequisites:** E request budgets; I correct generations/remapping; coordinate graph.go edits with K.

Reuse destination reachability across planner rounds and coalesce compatible builds. Move expensive graph rebuild/compaction/checkpoint work toward bounded immutable publication, compact strings/IDs with reference remapping, and avoid reconstructing temporary maps for sealed snapshots that do not need them.

These are promising structural changes, but transient duplication, lock ownership and snapshot compatibility require larger design work. Use lock-wait and peak-memory measurements to choose the first subtask.

**Completion checks:** Measure p95/p99 board/planning latency during rolling completion and slow snapshot writes, plus startup peak RSS and long-running retained heap. Verify route/tie results, concurrent fills, restart compatibility and ID remapping. Prepared graph sections must have a bounded extra-memory budget.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR070 | Reuse planner destination reachability across rounds | [departuregraph/corridor.go:241](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/corridor.go:241) |
| PR074 | Reduce graph-wide write-lock maintenance | [departuregraph/graph.go:735](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/graph.go:735) |
| PR075 | Compact interned strings and high-water capacities | [departuregraph/graph.go:767](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/graph.go:767) |
| PR085 | Shorten snapshot lock hold time | [departuregraph/snapshot.go:132](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/snapshot.go:132) |
| PR086 | Avoid needless construction maps on snapshot restore | [departuregraph/snapshot.go:235](/Users/aaronclaydon/projects/travigo/travigo/pkg/departuregraph/snapshot.go:235) |

<a id="set-q"></a>

### Set Q — Compact runtime payloads and notification work

**Expected payoff:** Potentially high at large route/subscription volumes; requires compatibility migrations. **Effort:** XL. **Prerequisites:** L committed immutable event state; I/J static versions and cache contracts.

Separate mutable runtime data from versioned static paths, serve compact map summaries, and publish compact versioned event snapshots that preserve event-time meaning. Narrow subscription candidates before expression evaluation and cache commute planning by commute, date and timetable generation with disruption-aware revalidation.

Payload size and subscription fan-out can dominate at scale, but storage, readers and expression consumers must migrate together. This is a later package unless measurements show these are the dominant costs.

**Completion checks:** Compare Redis/event bytes, decode allocations, candidates evaluated per event and commute planning work as load grows. Test old/new payload readers, event-expression compatibility, delayed event consumption, synthetic journeys and timetable expiry. A latest-state pointer must never replace an immutable event snapshot.

| ID | Finding | Affected comment |
| --- | --- | --- |
| PR090 | Index subscription candidates before expression evaluation | [events/consumer.go:52](/Users/aaronclaydon/projects/travigo/travigo/pkg/events/consumer.go:52) |
| PR091 | Cache commute plans by date and timetable generation | [events/subscription_cache.go:153](/Users/aaronclaydon/projects/travigo/travigo/pkg/events/subscription_cache.go:153) |
| PR122 | Publish compact immutable event snapshots | [realtime/realtimestore/events.go:62](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/realtimestore/events.go:62) |
| PR126 | Read compact map summaries | [realtime/realtimestore/reader.go:348](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/realtimestore/reader.go:348) |
| PR128 | Separate static paths from mutable runtime storage | [realtime/realtimestore/storage.go:146](/Users/aaronclaydon/projects/travigo/travigo/pkg/realtime/realtimestore/storage.go:146) |

## Coverage and change boundary

Every reviewed finding belongs to exactly one set. Cross-set dependencies are named above so the same change is not implemented twice. The accompanying source edit adds only `[Set X] [PRnnn]` to each review opening; it does not implement any proposed optimisation. The grouping edits and this plan are left unstaged, preserving the previously staged diff.
