package batchrunner

import "testing"

func TestConfigFromEnvDefaultsJobBackoffLimitToThree(t *testing.T) {
	t.Setenv("TRAVIGO_BATCH_JOB_BACKOFF_LIMIT", "")

	if got := ConfigFromEnv().JobBackoffLimit; got != 3 {
		t.Fatalf("JobBackoffLimit = %d, want 3", got)
	}
}
