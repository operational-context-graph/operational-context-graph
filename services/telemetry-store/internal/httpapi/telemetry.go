// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	v1 "github.com/operational-context-graph/operational-context-graph/services/telemetry-store/api/v1"
	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/apperr"
	"github.com/operational-context-graph/operational-context-graph/services/telemetry-store/internal/store"
)

const (
	routeQueryLogs    = "query-logs"
	routeQueryMetrics = "query-metrics"
)

func registerTelemetryRoutes(rg *gin.RouterGroup, d telemetryDB) {
	rg.GET("/logs", setRoute(routeQueryLogs), handleQueryLogs(d))
	rg.GET("/metrics", setRoute(routeQueryMetrics), handleQueryMetrics(d))
}

func handleQueryLogs(d telemetryDB) gin.HandlerFunc {
	return func(c *gin.Context) {
		q, err := parseTimeRange(c)
		if err != nil {
			renderError(c, fmt.Errorf("%w: %s", apperr.ErrInvalidInput, err.Error()))
			return
		}
		limit := parseLimit(c)
		lq := store.LogQuery{
			From:     q.from,
			To:       q.to,
			HostName: c.Query("host_name"),
			Severity: c.Query("severity"),
			Limit:    limit,
		}
		if after := c.Query("after"); after != "" {
			var cur store.Cursor
			cur, err = store.DecodeCursor(after)
			if err != nil {
				renderError(c, fmt.Errorf("%w: invalid cursor", apperr.ErrInvalidInput))
				return
			}
			lq.AfterTS = &cur.TS
			lq.AfterIRI = &cur.IRI
		}
		logs, err := d.QueryLogs(c.Request.Context(), lq)
		if err != nil {
			renderError(c, err)
			return
		}
		items := make([]v1.LogResponse, len(logs))
		for i, l := range logs {
			item := v1.LogResponse{
				Timestamp:          l.Timestamp.UTC().Format(time.RFC3339Nano),
				IRI:                l.IRI,
				HostName:           l.HostName,
				Severity:           l.Severity,
				Body:               l.Body,
				ResourceAttributes: l.ResourceAttributes,
				Attributes:         l.Attributes,
			}
			if l.ObservedTimestamp != nil {
				s := l.ObservedTimestamp.UTC().Format(time.RFC3339Nano)
				item.ObservedTimestamp = &s
			}
			items[i] = item
		}
		resp := v1.LogListResponse{Items: items}
		if len(logs) >= lq.Limit && lq.Limit > 0 {
			last := logs[len(logs)-1]
			cur := store.Cursor{TS: last.Timestamp, IRI: last.IRI}
			resp.NextCursor = cur.Encode()
		}
		c.JSON(http.StatusOK, resp)
	}
}

func handleQueryMetrics(d telemetryDB) gin.HandlerFunc {
	return func(c *gin.Context) {
		q, err := parseTimeRange(c)
		if err != nil {
			renderError(c, fmt.Errorf("%w: %s", apperr.ErrInvalidInput, err.Error()))
			return
		}
		limit := parseLimit(c)
		mq := store.MetricQuery{
			From:       q.from,
			To:         q.to,
			HostName:   c.Query("host_name"),
			MetricName: c.Query("metric_name"),
			Limit:      limit,
		}
		if after := c.Query("after"); after != "" {
			var cur store.Cursor
			cur, err = store.DecodeCursor(after)
			if err != nil {
				renderError(c, fmt.Errorf("%w: invalid cursor", apperr.ErrInvalidInput))
				return
			}
			mq.AfterTS = &cur.TS
			mq.AfterIRI = &cur.IRI
		}
		metrics, err := d.QueryMetrics(c.Request.Context(), mq)
		if err != nil {
			renderError(c, err)
			return
		}
		items := make([]v1.MetricResponse, len(metrics))
		for i, m := range metrics {
			item := v1.MetricResponse{
				Timestamp:          m.Timestamp.UTC().Format(time.RFC3339Nano),
				IRI:                m.IRI,
				HostName:           m.HostName,
				MetricName:         m.MetricName,
				Value:              m.Value,
				ResourceAttributes: m.ResourceAttributes,
				Attributes:         m.Attributes,
			}
			if m.ObservedTimestamp != nil {
				s := m.ObservedTimestamp.UTC().Format(time.RFC3339Nano)
				item.ObservedTimestamp = &s
			}
			items[i] = item
		}
		resp := v1.MetricListResponse{Items: items}
		if len(metrics) >= mq.Limit && mq.Limit > 0 {
			last := metrics[len(metrics)-1]
			cur := store.Cursor{TS: last.Timestamp, IRI: last.IRI}
			resp.NextCursor = cur.Encode()
		}
		c.JSON(http.StatusOK, resp)
	}
}

type timeRange struct {
	from time.Time
	to   time.Time
}

// parseTimeRange parses from/to query params.
// Both params are optional; omitting both applies a 1-hour default window.
// Providing only one is a 400 error.
func parseTimeRange(c *gin.Context) (timeRange, error) {
	fromStr := c.Query("from")
	toStr := c.Query("to")

	if fromStr == "" && toStr == "" {
		to := time.Now().UTC()
		return timeRange{from: to.Add(-1 * time.Hour), to: to}, nil
	}
	if fromStr == "" || toStr == "" {
		return timeRange{}, fmt.Errorf("provide both from and to, or neither")
	}
	from, err := time.Parse(time.RFC3339, fromStr)
	if err != nil {
		return timeRange{}, fmt.Errorf("invalid from: %w", err)
	}
	to, err := time.Parse(time.RFC3339, toStr)
	if err != nil {
		return timeRange{}, fmt.Errorf("invalid to: %w", err)
	}
	const maxSpan = 24 * time.Hour
	if to.Sub(from) > maxSpan {
		return timeRange{}, fmt.Errorf("time range exceeds maximum of 24 hours")
	}
	return timeRange{from: from, to: to}, nil
}

func parseLimit(c *gin.Context) int {
	n, _ := strconv.Atoi(c.Query("limit"))
	return n
}
