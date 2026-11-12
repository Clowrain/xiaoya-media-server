package migrations

import _ "embed"

//go:embed 002_resource_engine.sql
var ResourceEngine string

//go:embed 003_resource_lifecycle.sql
var ResourceLifecycle string
