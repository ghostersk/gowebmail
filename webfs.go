package gowebmail

import "embed"

// Global access to the web assets
//
//go:embed web/static/** web/templates/**
var WebFS embed.FS
