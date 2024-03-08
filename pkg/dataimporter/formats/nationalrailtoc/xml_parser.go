package nationalrailtoc

import (
	"encoding/xml"
	"io"
)

func (t *TrainOperatingCompanyList) ParseFile(reader io.Reader) error {
	byteValue, _ := io.ReadAll(reader)

	xml.Unmarshal(byteValue, &t)

	return nil
}
