package prometheusadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

// sampleCount returns the total histogram observation count across all
// series of the metric family named name.
func sampleCount(t *testing.T, reg *prometheus.Registry, name string) uint64 {
	t.Helper()
	family := metricFamily(t, reg, name)
	var total uint64
	for _, m := range family.GetMetric() {
		total += m.GetHistogram().GetSampleCount()
	}
	return total
}

func metricFamily(t *testing.T, reg *prometheus.Registry, name string) *dto.MetricFamily {
	t.Helper()
	f := findMetricFamily(t, reg, name)
	if f == nil {
		t.Fatalf("metric family %q not found", name)
	}
	return f
}

// findMetricFamily returns nil, rather than failing the test, if name has no
// series yet — a GaugeVec/HistogramVec with no observed label combination
// isn't reported by Gather at all, which is expected, not an error.
func findMetricFamily(t *testing.T, reg *prometheus.Registry, name string) *dto.MetricFamily {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() == name {
			return f
		}
	}
	return nil
}

func gaugeValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	family := metricFamily(t, reg, name)
	for _, m := range family.GetMetric() {
		got := make(map[string]string, len(m.GetLabel()))
		for _, l := range m.GetLabel() {
			got[l.GetName()] = l.GetValue()
		}
		match := true
		for k, v := range labels {
			if got[k] != v {
				match = false
				break
			}
		}
		if match {
			return m.GetGauge().GetValue()
		}
	}
	t.Fatalf("no series of %q matched labels %v", name, labels)
	return 0
}

func counterValue(t *testing.T, reg *prometheus.Registry, name, label, value string) float64 {
	t.Helper()
	family := metricFamily(t, reg, name)
	for _, m := range family.GetMetric() {
		for _, l := range m.GetLabel() {
			if l.GetName() == label && l.GetValue() == value {
				return m.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("no series of %q with %s=%q", name, label, value)
	return 0
}

func statusesSeen(t *testing.T, reg *prometheus.Registry, familyName string) map[string]bool {
	t.Helper()
	family := metricFamily(t, reg, familyName)
	statuses := make(map[string]bool)
	for _, m := range family.GetMetric() {
		for _, l := range m.GetLabel() {
			if l.GetName() == "status" {
				statuses[l.GetValue()] = true
			}
		}
	}
	return statuses
}

func TestObserver_RecordsWaitAndRunSamples(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs := New("test", reg)

	obs.ObserveAcquire("primary", 10*time.Millisecond, nil)
	obs.ObserveComplete("primary", 20*time.Millisecond, nil)

	obs.ObserveAcquire("primary", 5*time.Millisecond, nil)
	obs.ObserveComplete("primary", 15*time.Millisecond, errors.New("boom"))

	require.EqualValues(t, 2, sampleCount(t, reg, "test_throttle_wait_seconds"))
	require.EqualValues(t, 2, sampleCount(t, reg, "test_run_seconds"))
}

func TestObserver_RunLabelsOkCanceledAndError(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs := New("test", reg)

	obs.ObserveAcquire("primary", 0, nil)
	obs.ObserveComplete("primary", time.Millisecond, nil)

	obs.ObserveAcquire("primary", 0, nil)
	obs.ObserveComplete("primary", time.Millisecond, errors.New("connection refused"))

	obs.ObserveAcquire("primary", 0, nil)
	obs.ObserveComplete("primary", time.Millisecond, context.DeadlineExceeded)

	statuses := statusesSeen(t, reg, "test_run_seconds")
	require.True(t, statuses["ok"], "want a status=ok series")
	require.True(t, statuses["error"], "want a status=error series for a real failure")
	require.True(t, statuses["canceled"], "want a status=canceled series distinct from a real failure")
}

func TestObserver_WaitLabelsOkAndCanceled(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs := New("test", reg)

	obs.ObserveAcquire("primary", time.Millisecond, nil)
	obs.ObserveAcquire("primary", time.Millisecond, context.DeadlineExceeded)

	statuses := statusesSeen(t, reg, "test_throttle_wait_seconds")
	require.True(t, statuses["ok"])
	require.True(t, statuses["canceled"])
}

func TestObserver_DifferentSourcesGetDifferentSeries(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs := New("test", reg)

	obs.ObserveAcquire("primary", 0, nil)
	obs.ObserveComplete("primary", time.Millisecond, nil)
	obs.ObserveAcquire("replica", 0, nil)
	obs.ObserveComplete("replica", time.Millisecond, nil)

	family := metricFamily(t, reg, "test_run_seconds")
	sources := make(map[string]bool)
	for _, m := range family.GetMetric() {
		for _, l := range m.GetLabel() {
			if l.GetName() == "source" {
				sources[l.GetValue()] = true
			}
		}
	}
	require.True(t, sources["primary"])
	require.True(t, sources["replica"])
}

func TestObserver_InflightTracksAcquireCompletePairs(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs := New("test", reg)

	obs.ObserveAcquire("primary", 0, nil)
	require.Equal(t, float64(1), gaugeValue(t, reg, "test_inflight", map[string]string{"source": "primary"}))

	obs.ObserveAcquire("primary", 0, nil)
	require.Equal(t, float64(2), gaugeValue(t, reg, "test_inflight", map[string]string{"source": "primary"}))

	obs.ObserveComplete("primary", time.Millisecond, nil)
	require.Equal(t, float64(1), gaugeValue(t, reg, "test_inflight", map[string]string{"source": "primary"}))
}

func TestObserver_InflightNotIncrementedOnFailedAcquire(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs := New("test", reg)

	obs.ObserveAcquire("primary", 0, context.DeadlineExceeded)

	// A failed acquire must never touch the inflight gauge — since that's
	// the only thing that would create a "primary" series, the whole metric
	// family has no series at all yet, which Gather reports as absent
	// rather than as a family containing a zero-valued series.
	family := findMetricFamily(t, reg, "test_inflight")
	if family != nil {
		for _, m := range family.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "source" && l.GetValue() == "primary" {
					t.Fatalf("expected no inflight series for a failed acquire, got value %v", m.GetGauge().GetValue())
				}
			}
		}
	}
}

func TestObserver_SourceLifecycleTracksActiveGaugeAndEvents(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs := New("test", reg)

	obs.ObserveSourceRegistered("primary")
	obs.ObserveSourceRegistered("replica")
	require.Equal(t, float64(2), gaugeValue(t, reg, "test_sources_active", nil))
	require.Equal(t, float64(2), counterValue(t, reg, "test_source_events_total", "event", "registered"))

	obs.ObserveSourceRemoved("replica")
	require.Equal(t, float64(1), gaugeValue(t, reg, "test_sources_active", nil))
	require.Equal(t, float64(1), counterValue(t, reg, "test_source_events_total", "event", "removed"))
}

func TestObserver_SnapshotSetsGaugeAbsolutely(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs := New("test", reg)

	obs.ObserveSourceSnapshot([]string{"a", "b"})
	require.Equal(t, float64(2), gaugeValue(t, reg, "test_sources_active", nil))

	obs.ObserveSourceSnapshot([]string{"a", "b"}) // called again — must not double
	require.Equal(t, float64(2), gaugeValue(t, reg, "test_sources_active", nil))

	obs.ObserveSourceSnapshot([]string{"a"}) // fewer sources — must converge down too
	require.Equal(t, float64(1), gaugeValue(t, reg, "test_sources_active", nil))
}

func TestObserver_SnapshotDoesNotTouchEventsCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs := New("test", reg)

	obs.ObserveSourceSnapshot([]string{"a", "b"})

	family := findMetricFamily(t, reg, "test_source_events_total")
	require.Nil(t, family, "a snapshot must never create a source_events_total series")
}

// TestRegression_SetObserverCalledTwiceDoesNotLeaveGaugeStuck is the exact
// scenario from review: Open("primary") -> SetObserver(obs) ->
// SetObserver(obs) -> Remove("primary") must leave sources_active at 0, not
// 1 — reproduced here at the Observer level (Directory-level coverage is in
// internal/store/observer_test.go).
func TestRegression_SetObserverCalledTwiceDoesNotLeaveGaugeStuck(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs := New("test", reg)

	obs.ObserveSourceSnapshot([]string{"primary"}) // SetObserver #1
	obs.ObserveSourceSnapshot([]string{"primary"}) // SetObserver #2, same set
	obs.ObserveSourceRemoved("primary")            // Remove("primary")

	require.Equal(t, float64(0), gaugeValue(t, reg, "test_sources_active", nil))
}

func TestNew_SameNamespaceAndRegistryIsIdempotent(t *testing.T) {
	reg := prometheus.NewRegistry()
	first := New("test", reg)

	require.NotPanics(t, func() {
		second := New("test", reg)

		// Both instances must drive the same underlying series — an
		// observation through either one has to be visible from the other,
		// otherwise "idempotent" would just mean "doesn't panic" while
		// silently losing data from whichever Observer isn't used again.
		second.ObserveAcquire("primary", 0, nil)
		second.ObserveComplete("primary", time.Millisecond, nil)

		require.EqualValues(t, 1, sampleCount(t, reg, "test_run_seconds"))
		_ = first
	})
}

func TestNew_PanicsOnIncompatibleNameCollision(t *testing.T) {
	reg := prometheus.NewRegistry()
	// Register an unrelated metric under the exact name New would use for
	// sources_active, so New's registration for that name fails with an
	// existing collector of a different type it can't reuse.
	reg.MustRegister(prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "test",
		Name:      "sources_active",
		Help:      "not the gauge dbstore expects",
	}))

	require.Panics(t, func() {
		New("test", reg)
	})
}
