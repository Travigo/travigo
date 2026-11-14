package nrod

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-stomp/stomp/v3"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/railtd"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"go.mongodb.org/mongo-driver/bson"
)

const tdTrustWindow = 10 * time.Minute

var railHeadcode = regexp.MustCompile("^[0-9][A-Z][0-9]{2}$")

type tdEvent struct {
	Area        string `json:"area_id"`
	From        string `json:"from"`
	To          string `json:"to"`
	Description string `json:"descr"`
	Time        string `json:"time"`
}

type tdKey struct{ Area, From, To string }
type tdLeg struct{ Origin, Destination string }

// An empty From is a berth mapping. A nonempty From restricts it to a
// particular CA transition. Origin and Destination identify an ordered leg.
type tdMapping struct {
	Area, From, To       string
	Origin, Destination  string
	Confidence, Evidence string
	Point                ctdf.Location
	documentID           string
}

type tdAnchor struct {
	journeyID, trainID, headcode string
	runDate                      time.Time
	at, trustAt                  time.Time
	leg                          int
	area, berth                  string
	tdAt                         time.Time
	progress                     float64
}

type tdTracker struct {
	mu         sync.Mutex
	mappings   map[tdKey]map[tdLeg]tdMapping
	smart      map[tdKey]smartReportingPoint
	anchors    map[string]tdAnchor
	cancelled  map[string]time.Time
	areas      map[string]bool
	stats      map[string]*tdAreaStats
	lastReport time.Time
}

type tdAreaStats struct {
	seen, mapped, positioned, operational, ambiguous, rejected int
	maxAge                                                     time.Duration
	rejectionReasons                                           map[string]int
}

func newTDTracker() *tdTracker {
	return &tdTracker{anchors: map[string]tdAnchor{}, cancelled: map[string]time.Time{}, mappings: map[tdKey]map[tdLeg]tdMapping{}, areas: map[string]bool{}, stats: map[string]*tdAreaStats{}, lastReport: time.Now()}
}

func loadTD(ctx context.Context) (*tdTracker, error) {
	collection := database.GetCollection(database.TDMappingsCollectionName)
	cursor, err := collection.Find(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	tracker := newTDTracker()
	for cursor.Next(ctx) {
		documentID := cursor.Current.Lookup("_id").String()
		var m tdMapping
		if err := cursor.Decode(&m); err != nil {
			log.Warn().Err(err).Str("document_id", documentID).Msg("Skipping malformed TD mapping")
			continue
		}

		if m.Area == "" || m.To == "" || m.From == m.To || m.Origin == "" || m.Destination == "" ||
			strings.TrimSpace(m.Evidence) == "" || !railtd.ValidPoint(&m.Point) ||
			(m.Confidence != "mapped" && m.Confidence != "approximate") {
			log.Warn().Str("document_id", documentID).Msg("Skipping invalid TD mapping")
			continue
		}
		key := tdKey{m.Area, m.From, m.To}
		leg := tdLeg{m.Origin, m.Destination}
		byLeg := tracker.mappings[key]
		if byLeg == nil {
			byLeg = map[tdLeg]tdMapping{}
			tracker.mappings[key] = byLeg
		}
		if previous, exists := byLeg[leg]; exists {
			log.Warn().Str("document_id", documentID).Str("previous_document_id", previous.documentID).Msg("Ignoring conflicting TD mappings")
			// Keep the key reserved so a duplicate transition cannot fall back to a berth.
			byLeg[leg] = tdMapping{documentID: previous.documentID}
			continue
		}
		m.documentID = documentID
		byLeg[leg] = m
		tracker.areas[m.Area] = true
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return tracker, nil
}

func (t *tdTracker) trust(journey *ctdf.RealtimeJourney, m *TrustMovement, at time.Time, leg int, operationalStop *ctdf.Stop) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, a := range t.anchors {
		if time.Since(a.at) > tdTrustWindow {
			delete(t.anchors, id)
		}
	}
	for trainID, cancelledAt := range t.cancelled {
		if time.Since(cancelledAt) > tdTrustWindow {
			delete(t.cancelled, trainID)
		}
	}
	if cancelledAt, exists := t.cancelled[m.TrainID]; exists && !at.After(cancelledAt) {
		return
	}
	id := journey.PrimaryIdentifier
	old, exists := t.anchors[id]
	if exists && !at.After(old.trustAt) {
		return
	}
	if exists && operationalStop != nil && old.trainID == m.TrainID && old.runDate.Equal(journey.JourneyRunDate) &&
		journey.ActivelyTracked && !journey.Cancelled && m.CorrectionInd != "true" {
		origin, _ := journey.Journey.LegEndpoints(old.leg)
		if origin != nil && matchesPassingStop(origin.Outgoing, operationalStop) {
			old.trustAt = at
			t.anchors[id] = old
			return
		}
	}
	trainID := m.CurrentTrainID
	if trainID == "" {
		trainID = m.TrainID
	}
	headcode := ""
	if len(trainID) == 10 {
		headcode = trainID[2:6]
	}
	if !railHeadcode.MatchString(headcode) || !journey.ActivelyTracked || journey.Cancelled || journey.JourneyRunDate.IsZero() {
		leg = -1
	}
	// Keep a watermark even for arrivals, terminations and unresolved movements.
	t.anchors[id] = tdAnchor{journeyID: id, trainID: m.TrainID, headcode: headcode, runDate: journey.JourneyRunDate, at: at, trustAt: at, leg: leg}
}

func (t *tdTracker) invalidateTrain(trainID string, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if at.After(t.cancelled[trainID]) {
		t.cancelled[trainID] = at
	}
	for id, a := range t.anchors {
		if a.trainID == trainID && !at.Before(a.at) {
			if at.After(a.trustAt) {
				a.trustAt = at
			}
			a.leg = -1
			t.anchors[id] = a
		}
	}
}

func (s *StompClient) startTD(conn *stomp.Conn) {
	sub, err := conn.Subscribe("/topic/TD_ALL_SIG_AREA", stomp.AckAuto)
	if err != nil {
		log.Error().Err(err).Msg("Cannot subscribe to Network Rail TD")
		return
	}
	for msg := range sub.C {
		if msg == nil {
			break
		}
		if msg.Err != nil {
			log.Error().Err(msg.Err).Msg("Network Rail TD subscription failed")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.td.processFrame(ctx, msg.Body)
		cancel()
	}
	log.Warn().Msg("Network Rail TD subscription closed")
}

func (t *tdTracker) processFrame(ctx context.Context, data []byte) {
	var messages []map[string]tdEvent
	if err := json.Unmarshal(data, &messages); err != nil {
		log.Warn().Err(err).Msg("Invalid TD frame")
		return
	}
	for _, msg := range messages {
		if len(msg) != 1 {
			continue
		}
		for kind, event := range msg {
			switch kind {
			case "CA_MSG":
				if event.From == "" || event.To == "" || event.From == event.To {
					continue
				}
			case "CB_MSG":
				if event.From == "" {
					continue
				}
			case "CC_MSG":
				if event.To == "" {
					continue
				}
			default:
				continue
			}
			at, ok := railEventTime(event.Time, time.Now())
			if !ok || event.Area == "" {
				continue
			}
			if t.areas[event.Area] {
				t.process(ctx, kind, event, at)
			}
		}
	}
	if time.Since(t.lastReport) >= time.Minute {
		for area, stats := range t.stats {
			log.Info().Str("area", area).Int("seen", stats.seen).Int("mapped", stats.mapped).
				Int("positioned", stats.positioned).Int("operational", stats.operational).
				Int("ambiguous", stats.ambiguous).Int("rejected", stats.rejected).
				Dur("max_position_age", stats.maxAge).Interface("rejection_reasons", stats.rejectionReasons).Msg("TD observation counts")
		}
		clear(t.stats)
		t.lastReport = time.Now()
	}
}

func (t *tdTracker) process(ctx context.Context, kind string, event tdEvent, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := tdKey{event.Area, event.From, event.To}
	transitions := t.mappings[key]
	reportingPoint, hasSMART := t.smart[key]
	berths := t.mappings[tdKey{event.Area, "", event.To}]
	mapped := kind == "CA_MSG" && (len(transitions) != 0 || len(berths) != 0 || hasSMART)
	stats := t.stats[event.Area]
	if stats == nil {
		stats = &tdAreaStats{rejectionReasons: map[string]int{}}
		t.stats[event.Area] = stats
	}
	trace := func(outcome, reason, journeyID string) {
		log.Debug().Str("area", event.Area).Str("headcode", event.Description).Str("from", event.From).Str("to", event.To).
			Str("type", kind).Time("observed_at", at).Str("journey_id", journeyID).Str("outcome", outcome).Str("reason", reason).
			Msg("TD step decision")
	}
	reject := func(reason, journeyID string) {
		stats.rejected++
		stats.rejectionReasons[reason]++
		trace("rejected", reason, journeyID)
	}
	stats.seen++
	if mapped {
		stats.mapped++
	}
	validHeadcode := railHeadcode.MatchString(event.Description)
	var boundID string
	multipleBindings := false
	for id, a := range t.anchors {
		if event.From == "" || a.area != event.Area || a.berth != event.From || time.Since(a.at) > tdTrustWindow {
			continue
		}
		multipleBindings = boundID != ""
		boundID = id
	}
	for id, a := range t.anchors {
		if a.area != event.Area || a.berth == "" || !at.After(a.tdAt) {
			continue
		}
		// An overwrite, cancel, or unidentified movement breaks berth continuity.
		if a.berth == event.To || (a.berth == event.From && a.headcode != event.Description) {
			a.berth = ""
			a.leg = -1
			a.tdAt = at
			t.anchors[id] = a
		}
	}
	if kind == "CC_MSG" {
		return
	}
	if !validHeadcode {
		reject("invalid_headcode", "")
		return
	}
	if multipleBindings {
		stats.ambiguous++
		reject("multiple_berth_bindings", "")
		return
	}

	var selected tdAnchor
	var selectedMapping tdMapping
	var journey *ctdf.RealtimeJourney
	var position float64
	var reason, candidateID string
	noteRejection := func(nextReason, id string) {
		if reason == "" {
			reason, candidateID = nextReason, id
		} else {
			if reason != nextReason {
				reason = "multiple_candidate_rejections"
			}
			if candidateID != id {
				candidateID = ""
			}
		}
	}
	for id, a := range t.anchors {
		if (boundID != "" && id != boundID) || a.headcode != event.Description {
			continue
		}
		if a.leg < 0 || at.Sub(a.at) > tdTrustWindow || time.Since(a.at) > tdTrustWindow {
			noteRejection("no_recent_trust_departure", id)
			continue
		}
		if !at.After(a.trustAt) || !at.After(a.tdAt) {
			noteRejection("older_than_last_event", id)
			continue
		}
		continuous := a.area == event.Area && a.berth == event.From
		if !mapped && !continuous {
			noteRejection("unmapped_step", id)
			continue
		}
		candidate, err := realtimestore.FindForUpdate(ctx, a.journeyID)
		if err == redis.Nil {
			noteRejection("run_missing", id)
			continue
		}
		if err != nil {
			reject("run_lookup_failed", id)
			return
		}
		if candidate == nil || !candidate.ActivelyTracked || candidate.Cancelled ||
			candidate.OtherIdentifiers["TrainID"] != a.trainID || !candidate.JourneyRunDate.Equal(a.runDate) {
			noteRejection("run_inactive_or_changed", id)
			continue
		}
		progress := a.progress
		var mapping tdMapping
		if mapped {
			origin, destination := candidate.Journey.LegEndpoints(a.leg)
			if origin == nil {
				noteRejection("journey_leg_missing", id)
				continue
			}
			leg := origin.Outgoing
			legKey := tdLeg{origin.StopRef, destination.StopRef}
			var ok bool
			mapping, ok = transitions[legKey]
			if !ok {
				mapping, ok = berths[legKey]
			}
			if !ok && hasSMART {
				stopRef := origin.StopRef
				if reportingPoint.arrival {
					stopRef = destination.StopRef
				}
				if slices.Contains(reportingPoint.Refs, stopRef) || (leg != nil && slices.ContainsFunc(leg.PassingPath, func(ref string) bool {
					return slices.Contains(reportingPoint.Refs, ref)
				})) {
					mapping = tdMapping{From: event.From, Point: reportingPoint.Point, Confidence: "approximate"}
					ok = true
				}
			}
			if !ok || mapping.Confidence == "" {
				noteRejection("mapping_not_on_journey_leg", id)
				continue
			}
			var track []ctdf.Location
			if leg != nil {
				track = leg.Track
				if leg.TrackRef != "" {
					var record ctdf.JourneyTrack
					err := database.GetCollection("journey_tracks").FindOne(ctx, bson.M{"primaryidentifier": leg.TrackRef}).Decode(&record)
					if err != nil {
						reject("track_lookup_failed", id)
						return
					}
					track = record.Track
				}
			}
			progress = railtd.TrackProgress(mapping.Point, track)
			if progress < 0 {
				noteRejection("point_off_track_or_track_missing", id)
				continue
			}
			if mapping.From == "" {
				from, ok := t.mappings[tdKey{event.Area, "", event.From}][legKey]
				if !ok || from.Confidence == "" {
					noteRejection("source_berth_unmapped", id)
					continue
				}
				fromProgress := railtd.TrackProgress(from.Point, track)
				if fromProgress < 0 || progress <= fromProgress {
					noteRejection("source_berth_off_track_or_wrong_direction", id)
					continue
				}
			}
			if !a.tdAt.IsZero() && progress <= a.progress {
				noteRejection("position_did_not_advance", id)
				continue
			}
		}
		if journey != nil {
			stats.ambiguous++
			reject("multiple_matching_runs", "")
			return
		}
		selected, journey, position, selectedMapping = a, candidate, progress, mapping
	}
	if journey == nil {
		if reason == "" {
			reason = "no_matching_headcode"
			if !mapped && boundID == "" {
				reason = "unmapped_step"
			}
		}
		reject(reason, candidateID)
		return
	}
	if mapped {
		observation := realtimestore.RailObservation{
			ObservedAt: at, Source: "TD", Confidence: selectedMapping.Confidence, SignallingArea: event.Area, BerthID: event.To,
		}
		changed, err := realtimestore.RecordRailObservation(ctx, journey, observation, &selectedMapping.Point)
		if err != nil {
			log.Warn().Err(err).Str("journey_id", selected.journeyID).Msg("Failed to record TD observation")
			reject("position_write_failed", selected.journeyID)
			return
		}
		if changed {
			stats.positioned++
			stats.maxAge = max(stats.maxAge, time.Since(at))
			trace("positioned", selectedMapping.Confidence, selected.journeyID)
		} else {
			reject("position_not_newer", selected.journeyID)
		}
	} else {
		stats.operational++
		trace("berth_only", "", selected.journeyID)
	}
	if kind == "CB_MSG" {
		selected.leg = -1
	}
	selected.tdAt, selected.progress = at, position
	selected.area, selected.berth = event.Area, event.To
	t.anchors[selected.journeyID] = selected
}
