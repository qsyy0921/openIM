package observe

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "openim_platform", Subsystem: "http", Name: "requests_total",
		Help: "Platform API requests by stable route, method, and status.",
	}, []string{"route", "method", "status"})
	httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "openim_platform", Subsystem: "http", Name: "request_duration_seconds",
		Help:    "Platform API request latency by stable route and method.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "method"})
	collectorErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "openim_platform", Subsystem: "metrics", Name: "collection_errors_total",
		Help: "Operational PostgreSQL metric collection failures.",
	})
)

func init() {
	prometheus.MustRegister(httpRequests, httpDuration, collectorErrors)
}

func MetricsHandler() http.Handler { return promhttp.Handler() }

func HTTPMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &metricResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		httpRequests.WithLabelValues(route, r.Method, strconv.Itoa(recorder.status)).Inc()
		httpDuration.WithLabelValues(route, r.Method).Observe(time.Since(started).Seconds())
	})
}

type metricResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *metricResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *metricResponseWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

type operationalCollector struct {
	pool        *pgxpool.Pool
	queueItems  *prometheus.Desc
	queueAge    *prometheus.Desc
	mcpHealth   *prometheus.Desc
	remoteAgent *prometheus.Desc
}

func RegisterOperationalCollector(pool *pgxpool.Pool) error {
	return prometheus.Register(newOperationalCollector(pool))
}

func newOperationalCollector(pool *pgxpool.Pool) *operationalCollector {
	return &operationalCollector{
		pool:        pool,
		queueItems:  prometheus.NewDesc("openim_platform_queue_items", "Durable work items by queue and state.", []string{"queue", "state"}, nil),
		queueAge:    prometheus.NewDesc("openim_platform_queue_oldest_ready_seconds", "Age of the oldest ready work item.", []string{"queue"}, nil),
		mcpHealth:   prometheus.NewDesc("openim_platform_mcp_servers", "MCP server instances by health state.", []string{"state"}, nil),
		remoteAgent: prometheus.NewDesc("openim_platform_remote_a2a_agents", "Remote A2A Agents by lifecycle and enabled state.", []string{"state", "enabled"}, nil),
	}
}

func (c *operationalCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.queueItems
	ch <- c.queueAge
	ch <- c.mcpHealth
	ch <- c.remoteAgent
}

func (c *operationalCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rows, err := c.pool.Query(ctx, `
SELECT queue, state, count(*)::double precision
FROM (
    SELECT 'agent_runs' AS queue, state FROM agent.runs
    UNION ALL SELECT 'deliveries', state FROM agent.deliveries
    UNION ALL SELECT 'proactive_events', state FROM proactive.source_events
    UNION ALL SELECT 'memory_extractions', state FROM memory.extraction_jobs
    UNION ALL SELECT 'delegations', state FROM agent.delegations
    UNION ALL SELECT 'remote_a2a_jobs', state FROM agent.remote_a2a_jobs
    UNION ALL SELECT 'tool_approvals', state FROM agent.tool_approvals
) AS work
GROUP BY queue, state ORDER BY queue, state`)
	if err != nil {
		collectorErrors.Inc()
		return
	}
	for rows.Next() {
		var queue, state string
		var count float64
		if err := rows.Scan(&queue, &state, &count); err != nil {
			rows.Close()
			collectorErrors.Inc()
			return
		}
		ch <- prometheus.MustNewConstMetric(c.queueItems, prometheus.GaugeValue, count, queue, state)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		collectorErrors.Inc()
		return
	}
	rows.Close()

	ageRows, err := c.pool.Query(ctx, `
SELECT queue, age FROM (
    SELECT 'agent_runs' AS queue, COALESCE(extract(epoch FROM now() - min(available_at)), 0) AS age FROM agent.runs WHERE state IN ('queued', 'reply_pending') AND available_at <= now()
    UNION ALL SELECT 'deliveries', COALESCE(extract(epoch FROM now() - min(available_at)), 0) FROM agent.deliveries WHERE state = 'pending' AND available_at <= now()
    UNION ALL SELECT 'proactive_events', COALESCE(extract(epoch FROM now() - min(available_at)), 0) FROM proactive.source_events WHERE state IN ('pending', 'ranked') AND available_at <= now()
    UNION ALL SELECT 'memory_extractions', COALESCE(extract(epoch FROM now() - min(available_at)), 0) FROM memory.extraction_jobs WHERE state IN ('pending', 'projection_pending') AND available_at <= now()
    UNION ALL SELECT 'delegations', COALESCE(extract(epoch FROM now() - min(created_at)), 0) FROM agent.delegations WHERE state = 'queued'
    UNION ALL SELECT 'remote_a2a_jobs', COALESCE(extract(epoch FROM now() - min(created_at)), 0) FROM agent.remote_a2a_jobs WHERE state = 'queued'
    UNION ALL SELECT 'tool_approvals', COALESCE(extract(epoch FROM now() - min(created_at)), 0) FROM agent.tool_approvals WHERE state = 'requested'
) AS ages ORDER BY queue`)
	if err != nil {
		collectorErrors.Inc()
		return
	}
	for ageRows.Next() {
		var queue string
		var age float64
		if err := ageRows.Scan(&queue, &age); err != nil {
			ageRows.Close()
			collectorErrors.Inc()
			return
		}
		ch <- prometheus.MustNewConstMetric(c.queueAge, prometheus.GaugeValue, age, queue)
	}
	if err := ageRows.Err(); err != nil {
		ageRows.Close()
		collectorErrors.Inc()
		return
	}
	ageRows.Close()

	c.collectStateGauge(ctx, ch, c.mcpHealth, `SELECT state, count(*)::double precision FROM capability.mcp_health GROUP BY state`, false)
	c.collectStateGauge(ctx, ch, c.remoteAgent, `SELECT lifecycle_state, enabled::text, count(*)::double precision FROM agent.remote_agents GROUP BY lifecycle_state, enabled`, true)
}

func (c *operationalCollector) collectStateGauge(ctx context.Context, ch chan<- prometheus.Metric, desc *prometheus.Desc, query string, enabledLabel bool) {
	rows, err := c.pool.Query(ctx, query)
	if err != nil {
		collectorErrors.Inc()
		return
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count float64
		if enabledLabel {
			var enabled string
			if err := rows.Scan(&state, &enabled, &count); err != nil {
				collectorErrors.Inc()
				return
			}
			ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, count, state, enabled)
		} else {
			if err := rows.Scan(&state, &count); err != nil {
				collectorErrors.Inc()
				return
			}
			ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, count, state)
		}
	}
	if err := rows.Err(); err != nil {
		collectorErrors.Inc()
	}
}
