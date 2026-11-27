# NeTEx schedule and stop import plan

The `netex` dataset format imports scheduled passenger services and stop inventories into CTDF through the existing data importer. The first schedule fixtures are the French Aérobus A01 and A04 archives. Each archive separates stops, lines, calendars, and timetables into four XML files. [A01 dataset](https://transport.data.gouv.fr/resources/83580) · [A04 dataset](https://transport.data.gouv.fr/resources/83044)

## Implementation

1. **Read the dataset and resolve stops.** Parse standalone XML and ZIP bundles, indexing objects across all XML members before resolving references. Map `StopPlace` and `Quay` to CTDF stop groups and stops. Resolve journey calls through the `ScheduledStopPoint` and `PassengerStopAssignment` references. Support a stop-only inventory through `supportedobjects` and a separate schedule that supplies the matching NeTEx stop references through `linkeddataset`. [NeTEx schemas](https://github.com/TransmodelEcosystem/NeTEx) · [French stop profile](https://normes.transport.data.gouv.fr/normes/netex/arrets/) · [French network profile](https://normes.transport.data.gouv.fr/normes/netex/reseaux/)
2. **Convert fixed passenger schedules.** Map lines and operators to CTDF services and operators, and convert service journeys into ordered CTDF calls. Use the TransXChange and GTFS importers as references for stop activity, overnight times, and service availability. Resolve timetable calendars, date exceptions, and local timezones before writing. Return an import error when an advertised timetable cannot be represented faithfully. [French timetable profile](https://normes.transport.data.gouv.fr/normes/netex/horaires/) · [French shared objects profile](https://normes.transport.data.gouv.fr/normes/netex/elements_communs/)
3. **Integrate with the normal import lifecycle.** Register the format in the importer manager. Use the existing CTDF collections, dataset version, import report, and stale record cleanup. Treat a ZIP as one import so references can cross XML files and cleanup takes place after all writes succeed.
4. **Verify before enabling sources.** Exercise stop-only XML, cross-file references, missing references, calendar exceptions, and overnight calls. Run the two Aérobus archives and compare the converted stops, directions, journeys, and calendar coverage with their source data. Check for overlap with current GTFS datasets before enabling scheduled imports of either resource.

## Dataset configuration

Use `format: netex` and set `unpackbundle: zip` for ZIP archives. For a stop inventory, enable `stops` and `stopgroups` under `supportedobjects`. For a complete timetable with local stops, also enable `operators`, `services`, and `journeys`. For a timetable that references a separate NeTEx stop inventory, set `linkeddataset` to the fully qualified stop dataset identifier and enable the timetable objects. Set `customconfig.timezone` to an IANA timezone when the timetable XML omits its timezone, as the Aérobus files do.

The two Aérobus entries remain disabled because the current Île-de-France Mobilités GTFS already includes trips for both lines (`IDFM:C02179` and `IDFM:C02723`). Both formats would publish the same routes if enabled together. [Île-de-France Mobilités GTFS](https://transport.data.gouv.fr/resources/80921)

## Validation

Both Aérobus archives passed through the standard importer and journey linker in a local MongoDB. A01 produced 217 journeys and A04 produced 63. Each archive produced one service, one operator, four stop records, and two stop groups. Checks compared both directions, operating dates, overnight calls, line colours, and resolved stop references with the source XML. The extracted A01 stop file also imported as a standalone stop inventory.

The timetable importer handles fixed `ServiceJourney` schedules with explicit passing times. Frequency-based journey templates and unsupported calendar forms return an import error when journey import is enabled.
