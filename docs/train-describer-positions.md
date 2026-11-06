# Network Rail last reported train positions

The NROD tracker subscribes to `TD_ALL_SIG_AREA` on its existing Network Rail STOMP connection when SMART reporting points or verified mappings are available. TRUST movement reports continue to provide positions independently of TD. Journey coordinates retain the existing `VehicleLocation` shape. The map and journey endpoints add `VehicleLocationSource`, `VehicleLocationConfidence` and `VehicleLocationObservedAt` when available.

## Configure berth mappings

Store verified mapping documents in `gb_networkrail_td_mappings` in the same MongoDB database as the journeys. The database index setup creates the collection and enforces uniqueness across area, berths and directed leg. The NROD tracker loads the collection into memory at startup. Restart it after updating mappings. Keep one NROD consumer so recent TRUST correlation context stays with the TD consumer.

Each MongoDB document has `area`, `from`, `to`, `origin`, `destination`, `confidence`, `evidence`, and `point`. The origin and destination are ordered Travigo stop IDs. Confidence is `mapped` or `approximate`. Store the coordinates as `point: { type: "Point", coordinates: [longitude, latitude] }`. Set `from` to an empty string for a berth mapping. A populated `from` restricts the mapping to a specific CA step and takes precedence over berth mappings on the same leg. For a berth-only match, both berths need verified points on the same directed leg. CB clears a description and CC interposes one. Neither establishes a physical position by itself.

Positions require a single matching TRUST run, a departure in the previous ten minutes, and a point within 50 m of the ordered journey leg. Later points on the same leg must progress along the track. The 50 m check validates the mapping against OSM while retaining the reviewed coordinate. A matched unmapped step can maintain berth continuity in memory for later matching. Redis stores the last mapped point. The live map shows positions observed within the last ten minutes.

## Import SMART reporting points

Full batch runs import SMART after stop linking using the existing Network Rail credentials. To run the same import directly:

```sh
travigo data-importer td-mappings
```

Use `--smart SMART.json.gz` to load a local snapshot. The importer stores every SMART row nationwide in `gb_networkrail_td_smart` and resolves STANOX codes through the existing CORPUS links on Travigo stops. It publishes the completed snapshot in one MongoDB write. Restart NROD to load the snapshot.

A uniquely matched between-berth step can show an **approximate reporting point** when it resolves to one stop on the train's current leg and the point is within 50 m of that leg's OSM geometry. SMART timing offsets are retained with the source data. The position timestamp is the TD event time. Ambiguous steps and unresolved reporting locations remain in the snapshot without supplying a map point. Verified mappings take precedence where present.

For an optional corridor audit, the `td-mappings candidates` command accepts SMART and CORPUS files and defaults to Cambridge to King's Cross. For a verified override, `td-mappings --reviewed reviewed.csv --apply` takes columns `area,from,to,origin,destination,track_ref,longitude,latitude,confidence,evidence`. The importer validates those rows against the named OSM leg before writing them.

## Diagnose a position that is not updating

The tracker logs `TD observation counts` each minute for each active signalling area, including `rejection_reasons`. With `TRAVIGO_DEBUG=YES`, filter `TD step decision` messages by headcode or journey ID. Those messages show the source and destination berths, event timestamp, and outcome. A journey ID is available when the tracker could identify a single candidate run.

## Cambridge South to Foxton pilot

The 6 November 2026 SMART download contains two reporting anchors in Cambridge area `CA`: `0830 → 0120` for Shepreth Branch Junction (STANOX `47250`, offset `+20` seconds) and `0116 → 0114` for Foxton platform 1 (STANOX `47242`, offset `+110` seconds). The live TD feed reports the intervening steps `0120 → 0118` and `0118 → 0116`. Neither is in SMART for this area. The latter pair exists in area `ME` and refers to Gerrards Cross.

The [Cambridge PSB diagram](https://signalmaps.co.uk/#cambridge_psb:2941) places berths `0120`, `0118`, and `0116` beside signals `CA120`, `CA118`, and `CA116`, respectively. In the direction from Shepreth Branch Junction towards Foxton, the diagram shows `CA120` before Hauxton crossing, `CA118` between Hauxton and Harston crossings, and `CA116` between Harston crossing and Foxton. These are schematic relationships. They do not establish the physical trigger for each step.

OpenStreetMap extracts for the corridor contained railway geometry but no signal nodes in the surveyed bounding boxes. For point mappings, obtain a current Cambridge ITD stepping table or the commissioned signalling plan with signal or track circuit positions. The reference needs to reflect the [Stage 2 commissioning south of Cambridge](https://www.networkrail.co.uk/our-work/our-routes/anglia/improving-the-railway-in-anglia/cambridge-resignalling/) over Christmas 2025 and New Year 2026. If the plan gives line and mileage, use [Network Rail track and waymark data](https://findtransportdata.dft.gov.uk/dataset/track%2C-reference-line-and-organisational-boundaries-182fe21f794) to locate the trigger. Keep these steps unmapped as points until that check is possible.

References

- [Network Rail open data feeds](https://www.networkrail.co.uk/who-we-are/transparency-and-ethics/transparency/open-data-feeds/)
- [C-Class message format](https://wiki.openraildata.com/index.php/C_Class_Messages)
- [SMART and CORPUS fields](https://wiki.openraildata.com/index.php/Reference_data)
- [Network Rail Cambridgeshire Corridor Study](https://www.networkrail.co.uk/wp-content/uploads/2016/12/Cambridgeshire-Corridor-Study-2019.pdf)
