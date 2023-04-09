package naptan

func BasicFilter(object interface{}) bool {
	switch object.(type) {
	case *StopPoint:
		stopPoint := object.(*StopPoint)

		if stopPoint.StopType == "RSE" { // ignore rail entrances
			return false
		}
		// case *StopArea:
		// 	stopArea := object.(*StopArea)
	}

	return true
}
