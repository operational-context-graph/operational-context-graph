// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/operational-context-graph/telemetry-store/internal/apperr"
)

// problem is an RFC 9457 problem details object.
type problem struct {
	Type   string              `json:"type"`
	Title  string              `json:"title"`
	Status int                 `json:"status"`
	Detail string              `json:"detail,omitempty"`
	Errors []apperr.FieldError `json:"errors,omitempty"`
}

func renderError(c *gin.Context, err error) {
	var p problem
	var ve *apperr.ValidationError
	var maxErr *http.MaxBytesError
	switch {
	case errors.As(err, &maxErr):
		p = problem{
			Type:   "https://problems.ocg.example/payload-too-large",
			Title:  "Payload Too Large",
			Status: http.StatusRequestEntityTooLarge,
		}
	case errors.As(err, &ve) || errors.Is(err, apperr.ErrInvalidInput):
		p = problem{
			Type:   "https://problems.ocg.example/invalid-input",
			Title:  "Bad Request",
			Status: http.StatusBadRequest,
			Detail: err.Error(),
		}
		if ve != nil {
			p.Errors = ve.Fields
		}
	case errors.Is(err, apperr.ErrUnsupportedMediaType):
		p = problem{
			Type:   "https://problems.ocg.example/unsupported-media-type",
			Title:  "Unsupported Media Type",
			Status: http.StatusUnsupportedMediaType,
			Detail: err.Error(),
		}
	case errors.Is(err, apperr.ErrNotFound):
		p = problem{
			Type:   "https://problems.ocg.example/not-found",
			Title:  "Not Found",
			Status: http.StatusNotFound,
			Detail: err.Error(),
		}
	case errors.Is(err, apperr.ErrAlreadyExists), errors.Is(err, apperr.ErrConflict):
		p = problem{
			Type:   "https://problems.ocg.example/conflict",
			Title:  "Conflict",
			Status: http.StatusConflict,
			Detail: err.Error(),
		}
	case errors.Is(err, apperr.ErrLabelExists):
		// Label already committed — idempotent success, not an error.
		c.Header("Content-Type", "application/json")
		c.JSON(http.StatusOK, gin.H{"status": "already_exists"})
		return
	case errors.Is(err, apperr.ErrDorisOverloaded):
		c.Header("Retry-After", "5")
		p = problem{
			Type:   "https://problems.ocg.example/service-unavailable",
			Title:  "Service Unavailable",
			Status: http.StatusServiceUnavailable,
			Detail: "Doris is under memory pressure; retry after back-off",
		}
	case errors.Is(err, apperr.ErrDorisLoadFailed):
		p = problem{
			Type:   "https://problems.ocg.example/bad-gateway",
			Title:  "Bad Gateway",
			Status: http.StatusBadGateway,
			Detail: err.Error(),
		}
	default:
		route, _ := c.Get(routeKey)
		requestID := c.GetHeader("X-Request-Id")
		requestID = strings.ReplaceAll(requestID, "\n", "")
		requestID = strings.ReplaceAll(requestID, "\r", "")
		slog.Error("unhandled error", "err", err, "route", route, "request_id", requestID)
		p = problem{
			Type:   "https://problems.ocg.example/internal",
			Title:  "Internal Server Error",
			Status: http.StatusInternalServerError,
		}
	}
	c.Header("Content-Type", "application/problem+json")
	c.JSON(p.Status, p)
}
