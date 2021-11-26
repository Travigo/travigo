package naptan

func BusFilter(object interface{}) bool {
	switch object.(type) {
	case *StopPoint:
		stopType := (object.(*StopPoint)).StopType
		// BCT - On street Bus / Coach / Tram Stop. Public Use
		// BCS - Bus / Coach bay / stand / stance within Bus / Coach Stations
		return stopType == "BCT" || stopType == "BCS"
	case *StopArea:
		stopAreaType := (object.(*StopArea)).StopAreaType
		// GPBS - Paired on-street Bus / Coach / Tram stops
		// GCLS - Clustered on-street Bus / Coach / Tram stops
		// GBCS - Bus/Coach Station
		// GMLT - Multimode Interchange
		return stopAreaType == "GPBS" || stopAreaType == "GCLS" || stopAreaType == "GBCS"
	default:
		return false
	}
}
