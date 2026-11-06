package datasets

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

type DataSource struct {
	Identifier string    `groups:"web-datasource"`
	Region     string    `groups:"web-datasource"`
	Provider   Provider  `groups:"web-datasource"`
	Datasets   []DataSet `groups:"web-datasource"`

	SourceAuthentication *SourceAuthentication `json:"-"`
	Overrides            Overrides             `json:"-"`
}

func LoadDataSources() []DataSource {
	var sources []DataSource
	err := filepath.WalkDir("data/datasources", func(path string, file os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if file.IsDir() || filepath.Ext(path) != ".yaml" {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		decoder := yaml.NewDecoder(input)
		for {
			var source DataSource
			err = decoder.Decode(&source)
			if err != nil {
				break
			}
			sources = append(sources, source)
		}
		input.Close()
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("decode %s: %w", path, err)
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load datasources")
	}
	return sources
}
