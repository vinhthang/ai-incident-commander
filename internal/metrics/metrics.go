package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	agyDurationBounds = []float64{5, 15, 30, 60, 120, 180, 300}
	labelReplacer     = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
)

type proactiveKey struct {
	mode    string
	outcome string
}

type registry struct {
	mu                 sync.Mutex
	alertsTotal        map[string]uint64
	triageVerdicts     map[string]uint64
	remediationOutcome map[string]uint64
	dailyReports       map[string]uint64
	agyJobs            map[string]uint64
	proactiveFixes     map[proactiveKey]uint64
	agyBuckets         [7]uint64
	agyDurationSum     float64
	agyDurationCount   uint64
	queueDepthFn       func() int
}

var reg = newRegistry()

func newRegistry() *registry {
	return &registry{
		alertsTotal:        make(map[string]uint64),
		triageVerdicts:     make(map[string]uint64),
		remediationOutcome: make(map[string]uint64),
		dailyReports:       make(map[string]uint64),
		agyJobs:            make(map[string]uint64),
		proactiveFixes:     make(map[proactiveKey]uint64),
	}
}

func incCounter(m map[string]uint64, key string) {
	reg.mu.Lock()
	m[key]++
	reg.mu.Unlock()
}

// IncAlertsReceived increments commander_alerts_total for a webhook status.
func IncAlertsReceived(status string) {
	incCounter(reg.alertsTotal, status)
}

// IncTriageVerdict increments commander_triage_verdicts_total for a triage verdict.
func IncTriageVerdict(verdict string) {
	incCounter(reg.triageVerdicts, verdict)
}

// IncRemediationOutcome increments commander_remediation_outcomes_total for a remediation outcome.
func IncRemediationOutcome(outcome string) {
	incCounter(reg.remediationOutcome, outcome)
}

// IncDailyReport increments commander_daily_reports_total for a daily audit status.
func IncDailyReport(status string) {
	incCounter(reg.dailyReports, status)
}

// IncProactiveFix increments commander_proactive_fixes_total for a proactive daily remediation mode and outcome.
func IncProactiveFix(mode, outcome string) {
	reg.mu.Lock()
	reg.proactiveFixes[proactiveKey{mode: mode, outcome: outcome}]++
	reg.mu.Unlock()
}

// IncAgyJob increments commander_agy_jobs_total for an agy execution outcome.
func IncAgyJob(outcome string) {
	incCounter(reg.agyJobs, outcome)
}

// ObserveAgyDuration records an agy job duration in seconds.
func ObserveAgyDuration(seconds float64) {
	if seconds < 0 {
		seconds = 0
	}
	reg.mu.Lock()
	reg.agyDurationCount++
	reg.agyDurationSum += seconds
	for i, bound := range agyDurationBounds {
		if seconds <= bound {
			reg.agyBuckets[i]++
		}
	}
	reg.mu.Unlock()
}

// SetQueueDepthFunc registers a callback returning current AGY queue depth.
func SetQueueDepthFunc(fn func() int) {
	reg.mu.Lock()
	reg.queueDepthFn = fn
	reg.mu.Unlock()
}

// ResetForTest clears all counters, histogram buckets, and callbacks for unit tests.
func ResetForTest() {
	reg.mu.Lock()
	reg.alertsTotal = make(map[string]uint64)
	reg.triageVerdicts = make(map[string]uint64)
	reg.remediationOutcome = make(map[string]uint64)
	reg.dailyReports = make(map[string]uint64)
	reg.agyJobs = make(map[string]uint64)
	reg.proactiveFixes = make(map[proactiveKey]uint64)
	reg.agyBuckets = [7]uint64{}
	reg.agyDurationSum = 0
	reg.agyDurationCount = 0
	reg.queueDepthFn = nil
	reg.mu.Unlock()
}

// HandleMetrics serves Prometheus text exposition on GET /metrics.
func HandleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reg.mu.Lock()
	alerts := cloneMap(reg.alertsTotal)
	verdicts := cloneMap(reg.triageVerdicts)
	outcomes := cloneMap(reg.remediationOutcome)
	daily := cloneMap(reg.dailyReports)
	agyJobs := cloneMap(reg.agyJobs)
	proactive := make(map[proactiveKey]uint64, len(reg.proactiveFixes))
	for k, v := range reg.proactiveFixes {
		proactive[k] = v
	}
	buckets := reg.agyBuckets
	durSum := reg.agyDurationSum
	durCount := reg.agyDurationCount
	qFn := reg.queueDepthFn
	reg.mu.Unlock()

	queueDepth := 0
	if qFn != nil {
		queueDepth = qFn()
	}

	var b strings.Builder
	writeLabeledCounter(&b, "commander_alerts_total", "Total webhook alert items received by status.", "status", alerts)
	writeLabeledCounter(&b, "commander_triage_verdicts_total", "Total diagnostic triage outcomes by verdict.", "verdict", verdicts)
	writeLabeledCounter(&b, "commander_remediation_outcomes_total", "Total cluster remediation loop outcomes.", "outcome", outcomes)
	writeLabeledCounter(&b, "commander_daily_reports_total", "Total daily fleet audit executions by status.", "status", daily)
	writeLabeledCounter(&b, "commander_agy_jobs_total", "Total agy CLI worker jobs by outcome.", "outcome", agyJobs)
	writeProactiveCounter(&b, proactive)

	b.WriteString("# HELP commander_agy_duration_seconds Execution duration of agy CLI jobs in seconds.\n")
	b.WriteString("# TYPE commander_agy_duration_seconds histogram\n")
	for i, bound := range agyDurationBounds {
		fmt.Fprintf(&b, "commander_agy_duration_seconds_bucket{le=%q} %d\n", strconv.FormatFloat(bound, 'g', -1, 64), buckets[i])
	}
	fmt.Fprintf(&b, "commander_agy_duration_seconds_bucket{le=\"+Inf\"} %d\n", durCount)
	fmt.Fprintf(&b, "commander_agy_duration_seconds_sum %s\n", strconv.FormatFloat(durSum, 'g', -1, 64))
	fmt.Fprintf(&b, "commander_agy_duration_seconds_count %d\n", durCount)

	b.WriteString("# HELP commander_queue_depth Current number of queued agy CLI jobs.\n")
	b.WriteString("# TYPE commander_queue_depth gauge\n")
	fmt.Fprintf(&b, "commander_queue_depth %d\n", queueDepth)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}

func cloneMap(src map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func writeLabeledCounter(b *strings.Builder, name, help, labelKey string, values map[string]uint64) {
	fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	fmt.Fprintf(b, "# TYPE %s counter\n", name)
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "%s{%s=\"%s\"} %d\n", name, labelKey, labelReplacer.Replace(k), values[k])
	}
}

func writeProactiveCounter(b *strings.Builder, values map[proactiveKey]uint64) {
	b.WriteString("# HELP commander_proactive_fixes_total Total proactive remediations dispatched from daily audits by mode and outcome.\n")
	b.WriteString("# TYPE commander_proactive_fixes_total counter\n")
	keys := make([]proactiveKey, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].mode != keys[j].mode {
			return keys[i].mode < keys[j].mode
		}
		return keys[i].outcome < keys[j].outcome
	})
	for _, k := range keys {
		fmt.Fprintf(b, "commander_proactive_fixes_total{mode=\"%s\",outcome=\"%s\"} %d\n",
			labelReplacer.Replace(k.mode), labelReplacer.Replace(k.outcome), values[k])
	}
}
