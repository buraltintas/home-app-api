package observability

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/burakaltintas/home-app-api/internal/brand"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequests     = prometheus.NewCounterVec(prometheus.CounterOpts{Name: brand.MetricsNamespace + "_http_requests_total", Help: "HTTP requests by stable route, method and status."}, []string{"route", "method", "status"})
	httpDuration     = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: brand.MetricsNamespace + "_http_request_duration_seconds", Help: "HTTP request latency by stable route and method.", Buckets: prometheus.DefBuckets}, []string{"route", "method"})
	httpInFlight     = prometheus.NewGauge(prometheus.GaugeOpts{Name: brand.MetricsNamespace + "_http_requests_in_flight", Help: "HTTP requests currently being served."})
	authEvents       = prometheus.NewCounterVec(prometheus.CounterOpts{Name: brand.MetricsNamespace + "_auth_events_total", Help: "Authentication outcomes by bounded operation."}, []string{"operation", "outcome"})
	searches         = prometheus.NewCounterVec(prometheus.CounterOpts{Name: brand.MetricsNamespace + "_searches_total", Help: "Search requests by mode and outcome."}, []string{"mode", "outcome"})
	searchDuration   = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: brand.MetricsNamespace + "_search_duration_seconds", Help: "Search request latency.", Buckets: prometheus.DefBuckets}, []string{"mode"})
	zeroResults      = prometheus.NewCounter(prometheus.CounterOpts{Name: brand.MetricsNamespace + "_search_zero_results_total", Help: "Completed searches with no results."})
	providerRequests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: brand.MetricsNamespace + "_provider_requests_total", Help: "External provider calls by bounded provider and outcome."}, []string{"provider", "outcome"})
	providerDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: brand.MetricsNamespace + "_provider_request_duration_seconds", Help: "External provider call latency.", Buckets: prometheus.DefBuckets}, []string{"provider"})
	workerJobs       = prometheus.NewCounterVec(prometheus.CounterOpts{Name: brand.MetricsNamespace + "_worker_jobs_total", Help: "Background jobs by worker and outcome."}, []string{"worker", "outcome"})
	workerRetries    = prometheus.NewCounterVec(prometheus.CounterOpts{Name: brand.MetricsNamespace + "_worker_retries_total", Help: "Background job retry attempts."}, []string{"worker"})
	// What the search sufficiency gate decided, and why. The decision counter gives the
	// Local Only Rate and the Places Fallback Rate; the reason counter says which of the
	// gate's four conditions is driving the calls that remain, which is the difference
	// between "grow the catalogue" and "move a threshold".
	searchGate       = prometheus.NewCounterVec(prometheus.CounterOpts{Name: brand.MetricsNamespace + "_search_gate_decisions_total", Help: "Search sufficiency gate decisions."}, []string{"decision"})
	searchGateReason = prometheus.NewCounterVec(prometheus.CounterOpts{Name: brand.MetricsNamespace + "_search_gate_fallback_reasons_total", Help: "Conditions that sent a search to the provider. A search can fail more than one."}, []string{"reason"})
	// Shadow measurement: how often staying local cost the searcher a store the provider
	// would have shown above everything we did.
	searchShadow = prometheus.NewCounterVec(prometheus.CounterOpts{Name: brand.MetricsNamespace + "_search_shadow_measurements_total", Help: "Shadow provider calls made on local-only searches, by outcome."}, []string{"outcome"})
	searchStage  = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: brand.MetricsNamespace + "_search_stage_duration_seconds", Help: "Search latency by stage: the local query, the provider call, and the total for each path.", Buckets: prometheus.DefBuckets}, []string{"stage"})
)

func init() {
	prometheus.MustRegister(httpRequests, httpDuration, httpInFlight, authEvents, searches, searchDuration, zeroResults, providerRequests, providerDuration, workerJobs, workerRetries, searchGate, searchGateReason, searchShadow, searchStage)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpInFlight.Inc()
		defer httpInFlight.Dec()
		started := time.Now()
		rw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(rw, r)
		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}
		status := rw.status
		if status == 0 {
			status = http.StatusOK
		}
		httpRequests.WithLabelValues(route, r.Method, strconv.Itoa(status)).Inc()
		httpDuration.WithLabelValues(route, r.Method).Observe(time.Since(started).Seconds())
	})
}

func MetricsHandler(token string) http.Handler {
	next := promhttp.Handler()
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Metrics-Token")
		if len(got) != len(token) || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func Auth(operation, outcome string) { authEvents.WithLabelValues(operation, outcome).Inc() }

func Search(mode, outcome string, elapsed time.Duration, results int) {
	searches.WithLabelValues(mode, outcome).Inc()
	searchDuration.WithLabelValues(mode).Observe(elapsed.Seconds())
	if outcome == "success" && results == 0 {
		zeroResults.Inc()
	}
}

// SearchGate records one gate decision. Every failing condition is counted, not only the
// first, so the reasons add up to more than the fallbacks -- which is the point: a search
// can be short of results and short of coverage at the same time.
func SearchGate(localOnly bool, reasons []string) {
	if localOnly {
		searchGate.WithLabelValues("local_only").Inc()
		return
	}
	searchGate.WithLabelValues("places_fallback").Inc()
	for _, reason := range reasons {
		searchGateReason.WithLabelValues(reason).Inc()
	}
}

func SearchShadow(miss bool) {
	outcome := "no_miss"
	if miss {
		outcome = "high_relevance_miss"
	}
	searchShadow.WithLabelValues(outcome).Inc()
}

func SearchStage(stage string, elapsed time.Duration) {
	if elapsed <= 0 {
		return
	}
	searchStage.WithLabelValues(stage).Observe(elapsed.Seconds())
}

func Provider(provider, outcome string, elapsed time.Duration) {
	providerRequests.WithLabelValues(provider, outcome).Inc()
	providerDuration.WithLabelValues(provider).Observe(elapsed.Seconds())
}

func Worker(worker, outcome string, retry bool) {
	workerJobs.WithLabelValues(worker, outcome).Inc()
	if retry {
		workerRetries.WithLabelValues(worker).Inc()
	}
}

func Outcome(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	return "failure"
}
