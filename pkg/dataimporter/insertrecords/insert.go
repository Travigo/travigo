package insertrecords

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

func Insert() {
	err := filepath.Walk("data/insert-records/",
		func(path string, fileInfo os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if !fileInfo.IsDir() {
				log.Debug().Str("path", path).Msg("Loading insert-record file")

				transformYaml, err := os.ReadFile(path)
				if err != nil {
					return err
				}

				decoder := yaml.NewDecoder(bytes.NewReader(transformYaml))

				for {
					var insertDefinition InsertDefinition
					if decoder.Decode(&insertDefinition) != nil {
						break
					}

					insertDefinition.Upsert()
				}
			}

			return nil
		})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load insert-records directory")
	}
}
