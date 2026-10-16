<!--
SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors

SPDX-License-Identifier: Apache-2.0
-->

# Storage Security

The production PostgreSQL service MUST encrypt volumes, replicas, snapshots, and backups at rest.
The database platform owner MUST manage encryption keys outside the Schema Introspection API runtime.
The platform owner MUST retain audit logs, test point-in-time recovery, and verify restoration before production exposure.
The API module does not implement database encryption or backup operations.
