package util

import (
	"unicode"
	"unicode/utf8"
)

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

func CamelCaseSplit(src string) (entries []string) {
	// don't split invalid utf8
	if !utf8.ValidString(src) {
		return []string{src}
	}
	entries = []string{}
	var runes [][]rune
	lastClass := 0
	class := 0
	// split into fields based on class of unicode character
	for _, r := range src {
		switch true {
		case unicode.IsLower(r):
			class = 1
		case unicode.IsUpper(r):
			class = 2
		case unicode.IsDigit(r):
			class = 3
		default:
			class = 4
		}
		if class == lastClass {
			runes[len(runes)-1] = append(runes[len(runes)-1], r)
		} else {
			runes = append(runes, []rune{r})
		}
		lastClass = class
	}
	// handle upper case -> lower case sequences, e.g.
	// "PDFL", "oader" -> "PDF", "Loader"
	for i := 0; i < len(runes)-1; i++ {
		if unicode.IsUpper(runes[i][0]) && unicode.IsLower(runes[i+1][0]) {
			runes[i+1] = append([]rune{runes[i][len(runes[i])-1]}, runes[i+1]...)
			runes[i] = runes[i][:len(runes[i])-1]
		}
	}
	// construct []string from results
	for _, s := range runes {
		if len(s) > 0 {
			entries = append(entries, string(s))
		}
	}
	return
}
