# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0
.PHONY: proto-lint proto-format proto-generate

proto-lint:
	buf lint

proto-format:
	buf format -w

proto-generate:
	buf generate
