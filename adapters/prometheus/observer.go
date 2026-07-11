// Package prometheusadapter is a dbstore.Observer backed by Prometheus
// metrics. It has no knowledge of any backend client type — the same
// Observer can be attached to a sqlxadapter.Adapter, a restadapter.Adapter,
// or any other dbstore.Adapter[T] with SetObserver, since Observer only ever
// sees a source name, durations, and an error.
package prometheusadapter

import (
	"context"
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/loykin/dbstore"
)

// Observer records Directory/Executor internals as Prometheus metrics:
//   - {namespace}_throttle_wait_seconds{source,status}  histogram (status=ok|canceled)
//   - {namespace}_run_seconds{source,status}            histogram (status=ok|canceled|error)
//   - {namespace}_inflight{source}                       gauge — currently running Run calls
//   - {namespace}_sources_active                          gauge — currently registered sources
//   - {namespace}_source_events_total{event}              counter (event=registered|removed)
//
// "canceled" is split out from "error" on both histograms because it means
// something different for debugging: the caller's ctx gave up (deadline or
// explicit cancel), not that the backend or dbstore itself failed. Folding
// the two into one status would make it impossible to tell, from metrics
// alone, whether a spike is a slow backend or just callers with short
// timeouts.
type Observer struct {
	wait         *prometheus.HistogramVec
	run          *prometheus.HistogramVec
	inflight     *prometheus.GaugeVec
	sourcesTotal prometheus.Gauge
	sourceEvents *prometheus.CounterVec
}

var _ dbstore.Observer = (*Observer)(nil)

// dbOperationBuckets follows OpenTelemetry's semantic-convention recommended
// boundaries for db.client.operation.duration, not prometheus.DefBuckets —
// DefBuckets is documented as tuned for network service response times and
// starts at 5ms, which is too coarse for local/in-memory backends (SQLite,
// same-host Postgres) where sub-millisecond calls are common and would
// otherwise all fall in the first bucket.
var dbOperationBuckets = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10}

// New creates an Observer and registers its metrics under namespace (e.g.
// "myapp_sql") against reg. reg defaults to prometheus.DefaultRegisterer if
// nil — pass an app-owned *prometheus.Registry to share one /metrics
// endpoint with the app's own additional metrics.
//
// Calling New again with the same namespace and reg is safe and returns an
// Observer backed by the metrics already registered, instead of failing —
// e.g. test setup or app initialization code that (re)builds an Adapter more
// than once shouldn't have to special-case metrics registration. A genuine
// name collision with an incompatible metric (same name, different type)
// still panics: that's a programming error this package can't paper over.
func New(namespace string, reg prometheus.Registerer) *Observer {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	return &Observer{
		wait: registerOrReuse(reg, prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "throttle_wait_seconds",
			Help:      "Time Executor.Run spent waiting for a source's concurrency throttle, labeled by outcome.",
			Buckets:   dbOperationBuckets,
		}, []string{"source", "status"})),
		run: registerOrReuse(reg, prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "run_seconds",
			Help:      "Executor.Run's fn duration, labeled by outcome.",
			Buckets:   dbOperationBuckets,
		}, []string{"source", "status"})),
		inflight: registerOrReuse(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "inflight",
			Help:      "Run calls currently executing fn, by source.",
		}, []string{"source"})),
		sourcesTotal: registerOrReuse(reg, prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "sources_active",
			Help:      "Currently registered sources.",
		})),
		sourceEvents: registerOrReuse(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "source_events_total",
			Help:      "Source registration/removal events.",
		}, []string{"event"})),
	}
}

// registerOrReuse registers c and returns it, unless c's name is already
// registered with a collector of the same concrete type, in which case that
// existing collector is returned instead. Any other registration failure
// (including a name collision with an incompatible type) panics.
func registerOrReuse[C prometheus.Collector](reg prometheus.Registerer, c C) C {
	if err := reg.Register(c); err != nil {
		var already prometheus.AlreadyRegisteredError
		if errors.As(err, &already) {
			if existing, ok := already.ExistingCollector.(C); ok {
				return existing
			}
		}
		panic(err)
	}
	return c
}

// status categorizes err for a metric label: "ok" (nil), "canceled" (the
// caller's ctx was done), or "error" (anything else — a real backend or
// dbstore failure).
func status(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	default:
		return "error"
	}
}

// ObserveSourceSnapshot implements dbstore.Observer. It Sets sources_active
// to len(sources) directly, rather than incrementing once per element, so
// calling it more than once — SetObserver called again, or with a different
// Observer — always converges to the correct count instead of drifting
// upward. source_events_total is intentionally untouched here: a snapshot
// is a state sync, not a registration event, and must never inflate that
// counter no matter how many times SetObserver runs.
func (o *Observer) ObserveSourceSnapshot(sources []string) {
	o.sourcesTotal.Set(float64(len(sources)))
}

// ObserveSourceRegistered implements dbstore.Observer.
func (o *Observer) ObserveSourceRegistered(source string) {
	o.sourcesTotal.Inc()
	o.sourceEvents.WithLabelValues("registered").Inc()
}

// ObserveSourceRemoved implements dbstore.Observer.
func (o *Observer) ObserveSourceRemoved(source string) {
	o.sourcesTotal.Dec()
	o.sourceEvents.WithLabelValues("removed").Inc()
}

// ObserveAcquire implements dbstore.Observer. On success (err is nil) it
// also increments the in-flight gauge — ObserveComplete decrements it, so
// the pair brackets fn's execution.
func (o *Observer) ObserveAcquire(source string, waited time.Duration, err error) {
	o.wait.WithLabelValues(source, status(err)).Observe(waited.Seconds())
	if err == nil {
		o.inflight.WithLabelValues(source).Inc()
	}
}

// ObserveComplete implements dbstore.Observer.
func (o *Observer) ObserveComplete(source string, duration time.Duration, err error) {
	o.inflight.WithLabelValues(source).Dec()
	o.run.WithLabelValues(source, status(err)).Observe(duration.Seconds())
}
