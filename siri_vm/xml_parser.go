package siri_vm

import (
	"encoding/xml"
	"io"
)

func ParseXMLFile(reader io.Reader) (*SiriVM, error) {
	siriVM := SiriVM{}

	// d := xml.NewDecoder(util.NewValidUTF8Reader(reader))
	// d.DecodeElement()

	// TODO: stream me
	byteValue, _ := io.ReadAll(reader)
	xml.Unmarshal(byteValue, &siriVM)

	return &siriVM, nil
}
