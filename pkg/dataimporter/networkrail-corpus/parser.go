package networkrailcorpus

import (
	"encoding/json"
	"io"
)

func ParseJSONFile(reader io.Reader) (*Corpus, error) {
	var corpus Corpus

	byteValue, _ := io.ReadAll(reader)

	json.Unmarshal(byteValue, &corpus)

	return &corpus, nil
}
