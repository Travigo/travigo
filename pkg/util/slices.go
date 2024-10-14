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
