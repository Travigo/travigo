package transforms

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

var transforms []TransformDefinition

func SetupClient() {
	err := filepath.Walk("transforms/",
		func(path string, fileInfo os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if !fileInfo.IsDir() {
				log.Debug().Str("path", path).Msg("Loading transforms file")

				transformYaml, err := os.ReadFile(path)
				if err != nil {
					return err
				}

				decoder := yaml.NewDecoder(bytes.NewReader(transformYaml))

				for {
					var transformDefinition TransformDefinition
					if decoder.Decode(&transformDefinition) != nil {
						break
					}

					transforms = append(transforms, transformDefinition)
				}
			}

			return nil
		})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load transforms directory")
	}
}
