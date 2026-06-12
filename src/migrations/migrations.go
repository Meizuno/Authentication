// Package migrations holds the database migration SQL files, embedded into the
// binary so the service is self-contained and does not depend on the working
// directory or on shipping the .sql files alongside the image.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
