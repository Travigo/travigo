package util

func RemoveDuplicateStrings(strings []string, ignoreList []string) []string {
	presentStrings := make(map[string]bool)
	var list []string

	for _, ignoreString := range ignoreList {
		presentStrings[ignoreString] = true
	}

	for _, item := range strings {
		if _, value := presentStrings[item]; !value && item != "" {
			presentStrings[item] = true
			list = append(list, item)
		}
	}
	return list
}

func ContainsString(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}

	return false
}

func TrimString(s string, length int) string {
	if len(s) <= length {
		return s
	}

	return s[:length]
}
