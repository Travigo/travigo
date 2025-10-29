# Example Log Output

## Before Optimization

Every vehicle update resulted in a database write:

```
{"level":"info","Length":200,"Time":"1.2s","message":"Bulk write realtime_journeys"}
{"level":"info","Length":195,"Time":"1.1s","message":"Bulk write realtime_journeys"}
{"level":"info","Length":198,"Time":"1.3s","message":"Bulk write realtime_journeys"}
{"level":"info","Length":200,"Time":"1.2s","message":"Bulk write realtime_journeys"}
```

**Result**: ~200 writes per batch, ~1.2s per batch

## After Optimization

Most updates are now skipped due to no meaningful changes:

```
{"level":"info","consumer_id":0,"min_location_change_m":25,"min_bearing_change_deg":15,"max_time_between_writes":"5m0s","min_time_between_updates":"10s","message":"Initialized consumer with change detection config"}

{"level":"info","Length":45,"TotalEvents":200,"SkippedWrites":155,"PerformedWrites":45,"SkipRate%":77.5,"Time":"320ms","message":"Bulk write realtime_journeys"}
{"level":"info","Length":38,"TotalEvents":195,"SkippedWrites":157,"PerformedWrites":38,"SkipRate%":80.5,"Time":"285ms","message":"Bulk write realtime_journeys"}
{"level":"info","Length":52,"TotalEvents":198,"SkippedWrites":146,"PerformedWrites":52,"SkipRate%":73.7,"Time":"340ms","message":"Bulk write realtime_journeys"}
{"level":"info","TotalEvents":200,"SkippedWrites":200,"message":"No database writes needed - all updates skipped due to change detection"}
```

**Result**:
- ~45 writes per batch (77.5% reduction)
- ~320ms per batch (73% faster)
- Some batches skip entirely

## Debug Logs (when enabled)

Show exactly why writes are performed or skipped:

```
{"level":"debug","journey":"realtime-2025-01-15:abc123","reason":"next_stop_changed","message":"Writing to database"}
{"level":"debug","journey":"realtime-2025-01-15:def456","reason":"location_changed_45.2m","message":"Writing to database"}
{"level":"debug","journey":"realtime-2025-01-15:ghi789","reason":"bearing_changed_22.5°","message":"Writing to database"}
{"level":"debug","journey":"realtime-2025-01-15:jkl012","reason":"offset_changed","message":"Writing to database"}
{"level":"debug","journey":"realtime-2025-01-15:mno345","reason":"occupancy_changed_15%","message":"Writing to database"}
{"level":"debug","journey":"realtime-2025-01-15:pqr678","reason":"max_time_exceeded","message":"Writing to database"}

{"level":"debug","journey":"realtime-2025-01-15:stu901","reason":"no_significant_changes","message":"Skipping database write"}
{"level":"debug","journey":"realtime-2025-01-15:vwx234","reason":"too_soon","message":"Skipping database write"}
```

## Scenarios

### Scenario 1: Vehicle Moving Normally
Bus traveling at 30 km/h with GPS updates every 10 seconds:
- **Every ~10 seconds**: Update skipped (moved <25m in 10s at 30 km/h = ~83m, but bearing/stops unchanged)
- **Every ~30 seconds**: Update written (moved >25m)
- **Result**: ~67% reduction

### Scenario 2: Vehicle Stationary
Bus waiting at a stop with GPS updates every 10 seconds:
- **Every update**: Skipped (no movement, no changes)
- **Every 5 minutes**: Forced write (max time exceeded)
- **Result**: ~96% reduction

### Scenario 3: Vehicle Approaching Stop
Bus decelerating to stop:
- **Most updates**: Skipped (small movements)
- **When stop changes**: Written immediately (next_stop_changed)
- **Result**: Stop events captured perfectly, minor movements ignored

### Scenario 4: Rush Hour - High Occupancy Changes
Busy bus with frequent passenger boarding/alighting:
- **Occupancy changes >10%**: Written
- **Small occupancy changes**: Skipped
- **Result**: Captures meaningful occupancy trends

## Performance Impact

### Database Load
```
Before: 200 writes/batch × 5 batches/second = 1000 writes/second
After:  45 writes/batch × 5 batches/second = 225 writes/second
Reduction: 77.5%
```

### Processing Time
```
Before: 1.2s per batch = 6s total for 5 batches
After:  0.32s per batch = 1.6s total for 5 batches
Improvement: 73% faster
```

### Redis Usage
```
Active journeys: 5,000
Cache size per journey: ~1.5 KB
Total Redis memory: ~7.5 MB
Cost: Negligible
```

## Monitoring Queries

### Check Skip Rates (from logs)
```bash
# Using jq to analyze JSON logs
cat logs.json | jq -r 'select(.SkipRate != null) | .SkipRate' | awk '{sum+=$1; count++} END {print "Average skip rate:", sum/count "%"}'
```

### Find Write Reasons (from debug logs)
```bash
cat debug-logs.json | jq -r 'select(.reason != null) | .reason' | sort | uniq -c | sort -rn
```

Expected output:
```
  1250 no_significant_changes
   145 location_changed_*
    89 next_stop_changed
    45 offset_changed
    32 bearing_changed_*
    15 max_time_exceeded
    12 departed_stop_changed
     8 occupancy_changed_*
     4 new_journey
```
