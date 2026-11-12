# Data Sources

Travigo takes advantage of numerous different data sources to provide its functionality.

The currently supported data sources are:

* [NaPTAN](naptan.md) (Stops & StopGroups)
* [Traveline](traveline.md) (Operators & OperatorGroups)
* [Bus Open Data Service](busopendata.md)

## Batch import sizes

Set `datasetsize` using the sum of stops, stop groups, services, journeys,
journey tracks, operators, and operator groups in the dataset's latest
successful import report.

| Size | Reported objects | Smart mode concurrency |
| --- | ---: | ---: |
| `small` (default if omitted) | 0–9,999 | 3 |
| `medium` | 10,000–99,999 | 2 |
| `large` | 100,000 or more | 1 |

In the 12 November 2026 audit, the median was 1,343 objects. The 10,000 cutoff
put 516 of 624 measured datasets in the small group. The 100,000 cutoff
separated 25 datasets that accounted for 75% of all measured objects, leaving
83 in the middle. Object count is a scheduling proxy; import time and memory
also depend on the feed format and processing work.

From the repository root, run:

```sh
go run ./scripts/check-dataset-sizes.go
```

The checker reads the local YAML and queries the datasources API for the latest
reports. It lists mismatches and missing reports; use `-all` to see the full
audit and `-base-url` to check against another deployment. The checker exits
with status 1 for mismatches and 2 for API errors (build the script first if
you need those exact exit codes). A missing report leaves the size unverified.
Realtime and specific-runner datasets are outside the batch plan.
`enrichment` and `journey-enrichment` are scheduling stages and aren't
reclassified by object count.
