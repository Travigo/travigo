package naptan

func BasicFilter(object interface{}) bool {
	switch object.(type) {
	case *StopPoint:
		stopPoint := object.(*StopPoint)

		if stopPoint.StopClassification.StopType == "RSE" { // ignore rail entrances
			return false
		}
		if stopPoint.StopClassification.StopType == "TMU" { // ignore tramMetroOrUndergroundEntrance
			return false
		}
		// case *StopArea:
		// 	stopArea := object.(*StopArea)
	}

	return true
}
