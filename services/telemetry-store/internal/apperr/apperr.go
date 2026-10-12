// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package apperr

import (
	"errors"
	"strings"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrInvalidInput  = errors.New("invalid input")
	ErrConflict      = errors.New("conflict")
)

// FieldError describes a validation failure for a single request field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError carries per-field validation failures.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	msgs := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		msgs[i] = f.Field + ": " + f.Message
	}
	return "validation failed: " + strings.Join(msgs, "; ")
}
