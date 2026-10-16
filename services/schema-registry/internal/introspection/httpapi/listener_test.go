// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import "net"

func newListener() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}
