package util

func RemoveDuplicateStrings(strings []string) []string {
	presentStrings := make(map[string]bool)
	list := []string{}

	for _, item := range strings {
		if _, value := presentStrings[item]; !value {
			presentStrings[item] = true
			list = append(list, item)
		}
	}
	return list
}
