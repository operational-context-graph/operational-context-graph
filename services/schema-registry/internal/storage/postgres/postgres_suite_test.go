// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const postgresImage = "postgres:18"

// PostgreSQLSuite provisions a PostgreSQL container and applies all service
// migrations before each database-backed test.
type PostgreSQLSuite struct {
	suite.Suite

	Context     context.Context
	Container   *tcpostgres.PostgresContainer
	Database    *sql.DB
	DatabaseURL string
}

func (suite *PostgreSQLSuite) SetupSuite() {
	suite.Context = context.Background()

	container, err := tcpostgres.Run(
		suite.Context,
		postgresImage,
		tcpostgres.WithDatabase("schema_registry"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	suite.Require().NoError(err)
	suite.Container = container

	databaseURL, err := container.ConnectionString(suite.Context, "sslmode=disable")
	suite.Require().NoError(err)
	suite.DatabaseURL = databaseURL

	database, err := sql.Open("pgx", databaseURL)
	suite.Require().NoError(err)
	suite.Database = database
	suite.Require().NoError(database.PingContext(suite.Context))
	suite.Require().NoError(goose.SetDialect("postgres"))
}

func (suite *PostgreSQLSuite) SetupTest() {
	suite.Require().NoError(goose.UpContext(suite.Context, suite.Database, migrationDirectory()))
}

func (suite *PostgreSQLSuite) TearDownTest() {
	suite.Require().NoError(goose.DownContext(suite.Context, suite.Database, migrationDirectory()))
}

func (suite *PostgreSQLSuite) TearDownSuite() {
	if suite.Database != nil {
		suite.Require().NoError(suite.Database.Close())
	}
	if suite.Container != nil {
		suite.Require().NoError(suite.Container.Terminate(suite.Context))
	}
}

func migrationDirectory() string {
	return filepath.Join("..", "..", "..", "migrations")
}

func runPostgreSQLSuite(t *testing.T, testSuite suite.TestingSuite) {
	suite.Run(t, testSuite)
}
