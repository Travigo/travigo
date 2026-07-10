package routes

import "testing"

func TestRedisKeyCategory(t *testing.T) {
	tests := map[string]string{
		"cachedresults/departureboardjourneys/stop/hash/2026-07-10": "cache/cachedresults",
		"realtime-journey:details/realtime-2026":                    "realtime/journey-details",
		"realtime-journey:mapping:nationalrailrid:abc":              "realtime/journey-mappings",
		"realtime-journey:location/indexed":                         "realtime/locations",
		"realtime-journey:location:index":                           "realtime/location-index",
		"realtime-journeys:tfl-stop:123":                            "realtime/tfl-stop-indexes",
		"rmq::queue::[events-queue]::ready":                         "queue/ready",
		"rmq::queue::[events-queue]::rejected":                      "queue/rejected",
		"unknown:key":                                               "other",
	}

	for key, expected := range tests {
		if actual := redisKeyCategory(key); actual != expected {
			t.Errorf("redisKeyCategory(%q) = %q, want %q", key, actual, expected)
		}
	}
}

func TestParseRedisMemoryInfo(t *testing.T) {
	info := parseRedisMemoryInfo(`# Memory
used_memory:1234
used_memory_rss:2345
used_memory_peak:3456
maxmemory:100000
mem_fragmentation_ratio:1.25
maxmemory_policy:volatile-lru
`)

	if info.UsedMemoryBytes != 1234 || info.UsedMemoryRSSBytes != 2345 || info.PeakMemoryBytes != 3456 {
		t.Fatalf("unexpected memory values: %+v", info)
	}
	if info.MaxMemoryBytes != 100000 || info.FragmentationRatio != 1.25 || info.MaxMemoryPolicy != "volatile-lru" {
		t.Fatalf("unexpected Redis limits: %+v", info)
	}
}
