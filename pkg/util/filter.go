package util

func RemoveDuplicateStrings(strings []string, ignoreList []string) []string {
	presentStrings := make(map[string]bool)
	list := []string{}

	for _, ignoreString := range ignoreList {
		presentStrings[ignoreString] = true
	}

	for _, item := range strings {
		if _, value := presentStrings[item]; !value {
			presentStrings[item] = true
			list = append(list, item)
		}
	}
	return list
}
