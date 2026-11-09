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

// readFixture loads a canonical benchmark or fuzz input from the shared fixture tree.
func readFixture(tb testing.TB, name string) []byte {
	tb.Helper()
	// #nosec G304 -- name selects a repository-controlled fixture.
	data, err := os.ReadFile(filepath.Join("..", "fixtures", "manifest", name))
	require.NoError(tb, err)
	return bytes.TrimSuffix(data, []byte("\n"))
}
