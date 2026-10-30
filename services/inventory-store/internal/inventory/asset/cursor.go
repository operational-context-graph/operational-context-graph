// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package asset

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Cursor encodes the keyset pagination position for asset listing.
//
// It is a deliberate domain-side duplicate of the legacy store cursor, not an import of it: the
// repository stack must not depend on the store package. The JSON field tags ("t"/"i") and the
// base64 raw-URL encoding are kept identical so a cursor minted by either stack decodes in the
// other — the pagination token is part of the public wire contract, so both must agree on it.
type Cursor struct {
	CreatedAt time.Time `json:"t"`
	ID        uuid.UUID `json:"i"`
}

// Encode renders the cursor as an opaque URL-safe token.
func (c Cursor) Encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor parses a token produced by Encode.
func DecodeCursor(s string) (Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, err
	}
	var c Cursor
	return c, json.Unmarshal(b, &c)
}
