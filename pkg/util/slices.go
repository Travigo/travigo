package util

func InPlaceFilter[T any](s *[]T, p func(T) bool) {
	i := 0
	for _, e := range *s {
		if p(e) {
			(*s)[i] = e
			i++
		}
	}
	*s = (*s)[:i]
}

func SlicesOverlap(arr1, arr2 []string) bool {
	// Create a map to store elements from the first array
	elementSet := map[string]struct{}{}
	for _, val := range arr1 {
		elementSet[val] = struct{}{}
	}

	// Check if any element of the second array is present in the map
	for _, val := range arr2 {
		if _, exists := elementSet[val]; exists {
			return true
		}
	}
	return false
}
