package networkrailcorpus

import (
	"encoding/json"
	"io"
)

func (c *Corpus) ParseFile(reader io.Reader) error {
	// Performance Review [Set H] [PR048] [P1] (batch): Both read and JSON decode errors are
	// discarded. Malformed/partial input can become a successful empty or partial import,
	// allowing the manager to delete valid identifier mappings and publish its hash.
	// Propagate failures and validate the expected envelope/completeness before
	// writes/cleanup; preserve the last usable snapshot.
	// PERF note: whole-file ReadAll + Unmarshal is acceptable here - the CORPUS file is small, so streaming/decoding incrementally is not worth the change
	byteValue, _ := io.ReadAll(reader)

	json.Unmarshal(byteValue, &c)

	return nil
}
