# Realtime Vehicle Tracker Optimizations

## Overview

This document describes the database write optimizations implemented to reduce MongoDB write load for the realtime vehicle tracker system.

## Problem

The previous implementation was writing to MongoDB on every vehicle update, resulting in:
- Excessive database writes for minor position changes
- High database load and potential performance bottlenecks
- Unnecessary updates when vehicles were stationary or moving slowly
- Writes even when data hadn't meaningfully changed

## Solution

A multi-layered optimization approach:

### 1. Redis Caching Layer

**File**: `journeycache.go`

Introduced a Redis cache (`CachedRealtimeJourney`) that stores the current state of each realtime journey:
- Last known location and bearing
- Next stop and departed stop references
- Journey offset (delay/ahead)
- Occupancy status
- Timestamps for last update and last database write

This cache layer:
- Eliminates database reads during batch processing
- Enables comparison with previous state to detect meaningful changes
- Persists for 30 minutes (auto-expires when journey is no longer active)

### 2. Change Detection

**File**: `journeycache.go` - `ShouldWriteToDatabase()` method

Intelligent change detection that only writes to MongoDB when:

- **New journey** - First time we see this journey
- **Next stop changed** - Vehicle progressed to next stop (significant event)
- **Departed stop changed** - Vehicle departed from a stop
- **Offset changed** - Delay/early status changed significantly
- **Occupancy changed** - Bus capacity status changed
- **Location moved significantly** - Default: 25 meters threshold
- **Bearing changed significantly** - Default: 15 degrees threshold
- **Maximum time exceeded** - Force write every 5 minutes (ensures data freshness)

Updates are also rate-limited to a minimum of 10 seconds between any updates.

### 3. Configurable Thresholds

**File**: `config.go`

All thresholds are configurable via environment variables:

```bash
# Minimum distance change to trigger database write (meters)
REALTIME_MIN_LOCATION_CHANGE_METERS=25

# Minimum bearing change to trigger database write (degrees)
REALTIME_MIN_BEARING_CHANGE_DEGREES=15

# Force database write after this duration (e.g., "5m", "300s")
REALTIME_MAX_TIME_BETWEEN_WRITES=5m

# Minimum time between any updates (prevents rapid updates)
REALTIME_MIN_TIME_BETWEEN_UPDATES=10s
```

**Defaults:**
- `MinLocationChangeMeters`: 25.0 meters
- `MinBearingChangeDegrees`: 15.0 degrees
- `MaxTimeBetweenWrites`: 5 minutes
- `MinTimeBetweenUpdates`: 10 seconds

### 4. Metrics and Monitoring

**File**: `consumer.go`

Enhanced logging provides visibility into optimization effectiveness:

```
Bulk write realtime_journeys Length=45 TotalEvents=200 SkippedWrites=155 PerformedWrites=45 SkipRate%=77.5
```

Metrics tracked per batch:
- `TotalEvents`: Total vehicle updates processed
- `SkippedWrites`: Updates skipped due to no meaningful changes
- `PerformedWrites`: Updates written to MongoDB
- `SkipRate%`: Percentage of updates skipped
- `Time`: Duration of bulk write operation

## Performance Impact

**Expected Improvements:**
- **80-95% reduction** in MongoDB writes for most deployments
- Reduced database CPU and I/O load
- Lower MongoDB connection pool usage
- Faster batch processing (fewer database operations)

**Trade-offs:**
- Slightly increased Redis memory usage (~1-2KB per active journey)
- Minimal additional code complexity
- 10-second minimum update latency for non-significant changes

## Implementation Details

### Modified Files

1. **`journeycache.go`** (NEW)
   - Redis cache structure and operations
   - Change detection logic
   - Configuration structure

2. **`config.go`** (NEW)
   - Environment variable configuration
   - Default values

3. **`consumer.go`** (MODIFIED)
   - Initialize journey state cache
   - Add change detection config to BatchConsumer
   - Track and log metrics

4. **`realtimejourney.go`** (MODIFIED)
   - Check cached state before database reads
   - Apply change detection before writes
   - Update cache on every event

5. **`locationonly.go`** (MODIFIED)
   - Optimized location-only updates
   - Skip database reads when cache available
   - Apply same change detection logic

## Usage

### Default Behavior

No configuration needed - the system uses sensible defaults and will automatically reduce database writes.

### Tuning for Your Use Case

**High-frequency updates (e.g., every 5 seconds):**
```bash
REALTIME_MIN_LOCATION_CHANGE_METERS=50
REALTIME_MIN_BEARING_CHANGE_DEGREES=20
REALTIME_MIN_TIME_BETWEEN_UPDATES=15s
```

**Maximum data freshness (e.g., critical systems):**
```bash
REALTIME_MIN_LOCATION_CHANGE_METERS=10
REALTIME_MIN_BEARING_CHANGE_DEGREES=5
REALTIME_MAX_TIME_BETWEEN_WRITES=2m
```

**Maximum write reduction (e.g., cost optimization):**
```bash
REALTIME_MIN_LOCATION_CHANGE_METERS=100
REALTIME_MIN_BEARING_CHANGE_DEGREES=30
REALTIME_MAX_TIME_BETWEEN_WRITES=10m
REALTIME_MIN_TIME_BETWEEN_UPDATES=30s
```

## Monitoring

Watch the logs for skip rate percentages:
- **>90% skip rate**: Excellent - most updates are redundant
- **70-90% skip rate**: Good - optimization working well
- **50-70% skip rate**: Normal - vehicles are moving/changing frequently
- **<50% skip rate**: Review thresholds or check for data issues

## Future Enhancements

Potential improvements:
1. Separate hot/cold data paths (keep location in Redis only, periodic snapshots to MongoDB)
2. Adaptive thresholds based on vehicle speed
3. Per-operator or per-route threshold configuration
4. Prometheus metrics export
5. Background worker for periodic state snapshots
