package datalinker

import "testing"

func TestPlainCopyLinkerUsesRawStagingAndLiveCollections(t *testing.T) {
	linker := NewPlainCopyLinker("journey")
	live, raw, staging := linker.collectionNames()

	if live != "journeys" || raw != "journeys_raw" || staging != "journeys_staging" {
		t.Fatalf("collections = %q, %q, %q", live, raw, staging)
	}
}
