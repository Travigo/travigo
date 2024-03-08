package networkrailcorpus

import (
	"encoding/json"
	"io"
)

func (c *Corpus) ParseFile(reader io.Reader) error {
	byteValue, _ := io.ReadAll(reader)

	json.Unmarshal(byteValue, &c)

	return nil
}
