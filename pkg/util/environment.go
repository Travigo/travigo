package util

import (
	"os"
	"strings"
)

func GetEnvironmentVariables() map[string]string {
	environmentVariables := map[string]string{}

	for _, variable := range os.Environ() {
		pair := strings.SplitN(variable, "=", 2)

		environmentVariables[pair[0]] = pair[1]
	}

	return environmentVariables
}
