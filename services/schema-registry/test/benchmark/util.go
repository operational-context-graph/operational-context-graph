// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package benchmark

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// readFixture loads a canonical benchmark input from the shared fixture tree.
func readFixture(b *testing.B, name string) []byte {
	b.Helper()
	// #nosec G304 -- name selects a repository-controlled fixture.
	data, err := os.ReadFile(filepath.Join("..", "fixtures", "manifest", name))
	require.NoError(b, err)
	return bytes.TrimSuffix(data, []byte("\n"))
}
