package observability

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequests     = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "home_app_http_requests_total", Help: "HTTP requests by stable route, method and status."}, []string{"route", "method", "status"})
	httpDuration     = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "home_app_http_request_duration_seconds", Help: "HTTP request latency by stable route and method.", Buckets: prometheus.DefBuckets}, []string{"route", "method"})
	httpInFlight     = prometheus.NewGauge(prometheus.GaugeOpts{Name: "home_app_http_requests_in_flight", Help: "HTTP requests currently being served."})
	authEvents       = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "home_app_auth_events_total", Help: "Authentication outcomes by bounded operation."}, []string{"operation", "outcome"})
	searches         = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "home_app_searches_total", Help: "Search requests by mode and outcome."}, []string{"mode", "outcome"})
	searchDuration   = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "home_app_search_duration_seconds", Help: "Search request latency.", Buckets: prometheus.DefBuckets}, []string{"mode"})
	zeroResults      = prometheus.NewCounter(prometheus.CounterOpts{Name: "home_app_search_zero_results_total", Help: "Completed searches with no results."})
	providerRequests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "home_app_provider_requests_total", Help: "External provider calls by bounded provider and outcome."}, []string{"provider", "outcome"})
	providerDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "home_app_provider_request_duration_seconds", Help: "External provider call latency.", Buckets: prometheus.DefBuckets}, []string{"provider"})
	workerJobs       = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "home_app_worker_jobs_total", Help: "Background jobs by worker and outcome."}, []string{"worker", "outcome"})
	workerRetries    = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "home_app_worker_retries_total", Help: "Background job retry attempts."}, []string{"worker"})
)

func init() {
	prometheus.MustRegister(httpRequests, httpDuration, httpInFlight, authEvents, searches, searchDuration, zeroResults, providerRequests, providerDuration, workerJobs, workerRetries)
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
