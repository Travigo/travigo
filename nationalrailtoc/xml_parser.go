package nationalrailtoc

import (
	"encoding/xml"
	"io"
	"io/ioutil"
)

func ParseXMLFile(reader io.Reader) (*TrainOperatingCompanyList, error) {
	var tocList TrainOperatingCompanyList

	byteValue, _ := ioutil.ReadAll(reader)

	xml.Unmarshal(byteValue, &tocList)

	return &tocList, nil
}
