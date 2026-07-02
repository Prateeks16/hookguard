// Package ui embeds the Console's templates and static assets into the
// binary so deployment is a single file (DESIGN.md §7.6).
package ui

import "embed"

//go:embed templates
var TemplatesFS embed.FS

//go:embed static
var StaticFS embed.FS
