package nationalrailtoc

import (
	"encoding/xml"
	"io"
)

func ParseXMLFile(reader io.Reader) (*TrainOperatingCompanyList, error) {
	var tocList TrainOperatingCompanyList

	byteValue, _ := io.ReadAll(reader)

	xml.Unmarshal(byteValue, &tocList)

	return &tocList, nil
}
