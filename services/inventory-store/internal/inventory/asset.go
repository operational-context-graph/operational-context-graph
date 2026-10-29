// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

// Package inventory holds the DB-agnostic domain model for the inventory-store.
//
// The domain types here never import a database driver. They are constructed only
// through the reconstitution factory (see reconstitute.go), which guards the temporal
// invariants so an invalid Asset cannot exist in memory.
package inventory

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// NaturalKey is the business identity of an asset: the (source_system, source_id) pair.
// It is stable across re-versioning, unlike the row UUID.
type NaturalKey struct {
	SourceSystem string
	SourceID     string
}

// IsZero reports whether either component is empty.
func (k NaturalKey) IsZero() bool { return k.SourceSystem == "" || k.SourceID == "" }

// Validity is the system-time window during which an asset version is the current truth.
// An open window (To == nil) marks the live version; a closed window marks a superseded one.
type Validity struct {
	From time.Time
	To   *time.Time
}

// IsOpen reports whether this version is the current (non-expired) one.
func (v Validity) IsOpen() bool { return v.To == nil }

// Asset is the domain aggregate for an inventory asset version.
// Fields are unexported: callers read through accessors and construct only via the factory,
// so the temporal invariants enforced in reconstitute.go always hold.
type Asset struct {
	id       uuid.UUID
	key      NaturalKey
	iri      *string
	name     *string
	artifact string

	extensions json.RawMessage

	ingestedAt          *time.Time
	createdAt           time.Time
	createdAtSource     *time.Time
	lastUpdatedAtSource *time.Time

	validity Validity
}

// ID returns the version UUID (primary key of this row).
func (a Asset) ID() uuid.UUID { return a.id }

// Key returns the natural key.
func (a Asset) Key() NaturalKey { return a.key }

// IRI returns the asset IRI, or nil when unset.
func (a Asset) IRI() *string { return a.iri }

// Name returns the asset name, or nil when unset.
func (a Asset) Name() *string { return a.name }

// ArtifactType returns the artifact type.
func (a Asset) ArtifactType() string { return a.artifact }

// Extensions returns the raw JSON extensions payload.
func (a Asset) Extensions() json.RawMessage { return a.extensions }

// IngestedAt returns the ingestion timestamp, or nil when unset.
func (a Asset) IngestedAt() *time.Time { return a.ingestedAt }

// CreatedAt returns the row creation timestamp (system time).
func (a Asset) CreatedAt() time.Time { return a.createdAt }

// CreatedAtSource returns the source-reported creation time, or nil when unset.
func (a Asset) CreatedAtSource() *time.Time { return a.createdAtSource }

// LastUpdatedAtSource returns the source-reported last-update time, or nil when unset.
func (a Asset) LastUpdatedAtSource() *time.Time { return a.lastUpdatedAtSource }

// Validity returns the system-time validity window.
func (a Asset) Validity() Validity { return a.validity }

// IsOpen reports whether this is the current version.
func (a Asset) IsOpen() bool { return a.validity.IsOpen() }
