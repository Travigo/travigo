package siri_vm

import (
	"encoding/xml"
	"io"

	"github.com/adjust/rmq/v4"
	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/rs/zerolog/log"
)

func ParseXMLFile(reader io.Reader, queue rmq.Queue, datasource *ctdf.DataSource) error {
	retrievedRecords := 0
	submittedRecords := 0

	d := xml.NewDecoder(reader)
	for {
		tok, err := d.Token()
		if tok == nil || err == io.EOF {
			// EOF means we're done.
			break
		} else if err != nil {
			log.Fatal().Msgf("Error decoding token: %s", err)
			return err
		}

		switch ty := tok.(type) {
		case xml.StartElement:
			if ty.Name.Local == "VehicleActivity" {
				var vehicleActivity VehicleActivity

				if err = d.DecodeElement(&vehicleActivity, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					retrievedRecords += 1

					successfullyPublished := SubmitToProcessQueue(queue, &vehicleActivity, datasource)

					if successfullyPublished {
						submittedRecords += 1
					}
				}
			}
		}
	}

	log.Info().Int("retrieved", retrievedRecords).Int("submitted", submittedRecords).Msgf("Parsed latest Siri-VM response")

	return nil
}
