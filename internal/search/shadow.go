package search

import (
	"context"
	"log/slog"
	"time"

	"github.com/burakaltintas/home-app-api/internal/i18n"
	"github.com/burakaltintas/home-app-api/internal/observability"
	"github.com/google/uuid"
)

// Shadow measurement exists because the gate's own numbers cannot answer the only
// question that matters about it: when we decided not to ask the provider, was the
// provider holding something we should have shown?
//
// So a small sample of local-only searches is asked anyway -- after the person already
// has their answer, off the request path, and with nothing done to the result. The
// catalogue is deliberately not taught from it: a measurement that changes the system it
// measures is not a measurement.
//
// The sample stays small on purpose. A shadow call costs exactly what a real one costs,
// so measuring every local-only search would spend the entire saving on proving it.

const shadowTimeout = 20 * time.Second

type shadowMeasurement struct {
	SearchID         uuid.UUID
	Intent           Intent
	Request          Request
	Locale           i18n.Locale
	LocalResultCount int
	LocalTopScore    float64
	Coverage         int
	City, District   string
}

// shadowSampled draws the lottery for one search.
func (s *Service) shadowSampled() bool {
	if !s.policy.Enabled || s.policy.ShadowRate <= 0 || s.sample == nil {
		return false
	}
	if s.policy.ShadowRate >= 1 {
		return true
	}
	return s.sample() < s.policy.ShadowRate
}

func (s *Service) measureShadow(ctx context.Context, m shadowMeasurement) {
	ctx, cancel := context.WithTimeout(ctx, shadowTimeout)
	defer cancel()
	started := time.Now()
	places, reason := s.providerSearch(ctx, m.Intent, m.Request, m.Locale)
	elapsed := time.Since(started)
	if reason != "" {
		return
	}
	// A read, not an import. This is the one place in the search where a provider answer
	// is looked at without the catalogue learning from it.
	known, e := s.lookupExternal(ctx, places)
	if e != nil {
		slog.Default().Warn("shadow measurement lookup failed", "error", e, "search_id", m.SearchID)
		return
	}
	onlyCount, highCount := 0, 0
	maxScore := 0.0
	for rank, p := range places {
		if _, held := known[p.PlaceID]; held {
			continue
		}
		onlyCount++
		// Scored the way the search itself would have scored it, distance penalty and
		// all, so "better than what we showed" means the same thing here as it does in
		// the ranking a person actually sees.
		score := googleScore(p, rank)
		if m.Request.Latitude != nil && m.Request.Longitude != nil {
			score -= haversine(*m.Request.Latitude, *m.Request.Longitude, p.Latitude, p.Longitude) / 10000
		}
		if score > maxScore {
			maxScore = score
		}
		if score > m.LocalTopScore {
			highCount++
		}
	}
	if s.db != nil {
		_, e = s.db.Exec(ctx, `INSERT INTO search_shadow_measurements(search_id,city,district,local_result_count,local_top_score,catalogue_coverage,provider_result_count,provider_only_count,provider_only_max_score,provider_only_high_relevance_count,provider_duration_ms)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(search_id) DO NOTHING`,
			m.SearchID, nilIf(m.City == "", m.City), nilIf(m.District == "", m.District),
			m.LocalResultCount, m.LocalTopScore, nilIf(m.Coverage == coverageUnknown, m.Coverage),
			len(places), onlyCount, maxScore, highCount, elapsed.Milliseconds())
		if e != nil {
			slog.Default().Warn("shadow measurement write failed", "error", e, "search_id", m.SearchID)
		}
	}
	// The headline number: a miss is a search where staying local cost the person a
	// store that would have outranked everything they were shown.
	observability.SearchShadow(highCount > 0)
	observability.SearchStage("shadow", elapsed)
}
