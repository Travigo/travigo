package networkrailcorpus

import (
	"encoding/json"
	"io"
)

func (c *Corpus) ParseFile(reader io.Reader) error {
	// PERF note: whole-file ReadAll + Unmarshal is acceptable here - the CORPUS
	// file is small, so streaming/decoding incrementally is not worth the change.
	byteValue, _ := io.ReadAll(reader)

	json.Unmarshal(byteValue, &c)

	return nil
}
