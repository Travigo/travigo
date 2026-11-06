# Network Rail last reported train positions

The NROD tracker subscribes to `TD_ALL_SIG_AREA` on its existing Network Rail STOMP connection when reviewed mappings are available. TRUST movement reports continue to provide positions independently of TD. Journey coordinates retain the existing `VehicleLocation` shape. The map and journey endpoints add `VehicleLocationSource`, `VehicleLocationConfidence` and `VehicleLocationObservedAt` when available.

## Configure berth mappings

Store verified mapping documents in `gb_networkrail_td_mappings` in the same MongoDB database as the journeys. The database index setup creates the collection and enforces uniqueness across area, berths and directed leg. The NROD tracker loads the collection into memory at startup. Restart it after updating mappings. Keep one NROD consumer so recent TRUST correlation context stays with the TD consumer.

Each MongoDB document has `area`, `from`, `to`, `origin`, `destination`, `confidence`, `evidence`, and `point`. The origin and destination are ordered Travigo stop IDs. Confidence is `mapped` or `approximate`. Store the coordinates as `point: { type: "Point", coordinates: [longitude, latitude] }`. Set `from` to an empty string for a berth mapping. A populated `from` restricts the mapping to a specific CA step and takes precedence over berth mappings on the same leg. For a berth-only match, both berths need verified points on the same directed leg. CB clears a description and CC interposes one. Neither establishes a physical position by itself.

Positions require a single matching TRUST run, a departure in the previous ten minutes, and a point within 50 m of the ordered journey leg. Later points on the same leg must progress along the track. The 50 m check validates the mapping against OSM while retaining the reviewed coordinate. A matched unmapped step can maintain berth continuity in memory for later matching. Redis stores the last mapped point. The live map shows positions observed within the last ten minutes.

## Import the Cambridge to London King's Cross pilot

Download and decompress the SMART and CORPUS snapshots. Generate a candidate list with:

```sh
travigo data-importer td-mappings candidates --smart SMART.json --corpus CORPUS.json --output candidates.csv
```

The default CRS list covers Cambridge and intermediate calls towards King's Cross. Use `--stations` with a comma-separated list to select a different scope. The list is based on reporting locations, so it is a shortlist and can include steps from other branches at shared stations. The command keeps between-berth SMART steps and includes the reporting STANOX, TIPLOC, CRS and offset for review.

For each selected step, verify the physical trigger and its direction from signalling references and obtain its geographic coordinates. The SMART reporting location is a clue. Its coordinates cannot establish the position of a berth. Put only reviewed steps into a CSV with this header:

```csv
area,from,to,origin,destination,track_ref,longitude,latitude,confidence,evidence
```

Use Travigo stop IDs for the ordered `origin` and `destination`. The importer resolves stop aliases to the canonical endpoints of the selected leg. Use the `trackref` of the matching OSM leg from `journey_track_routes`. Include the signalling reference, coordinate source and review date in `evidence`. Keep berth identifiers as text to preserve leading zeroes.

```sh
travigo data-importer td-mappings --reviewed reviewed.csv
travigo data-importer td-mappings --reviewed reviewed.csv --apply
```

The first command validates the entire batch against the directed OSM legs in MongoDB. With `--apply`, the importer upserts the validated steps and leaves other mappings in place. Full batch runs use the reviewed CSV at `data/td-mappings/reviewed.csv`, shipped with the importer. They validate and import its rows after the OSM rail tracks and before journey publication. A CSV containing only the header has no mappings to import. Restart NROD to load new entries after either import method.

Record TD and TRUST across complete runs in both directions. Compare the number and age of valid positions with TRUST alone, check points against the signalling reference, and inspect rejected and ambiguous matches before widening coverage. The tracker logs one-minute observation counts for configured areas. The collection starts empty until verified mappings are added.

References

- [Network Rail open data feeds](https://www.networkrail.co.uk/who-we-are/transparency-and-ethics/transparency/open-data-feeds/)
- [C-Class message format](https://wiki.openraildata.com/index.php/C_Class_Messages)
- [SMART and CORPUS fields](https://wiki.openraildata.com/index.php/Reference_data)
- [Network Rail Cambridgeshire Corridor Study](https://www.networkrail.co.uk/wp-content/uploads/2016/12/Cambridgeshire-Corridor-Study-2019.pdf)
