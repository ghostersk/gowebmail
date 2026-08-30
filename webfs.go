package gowebmail

import "embed"

// Global access to the web assets
//
//go:embed web/static/css/* web/static/js/* web/static/img/* web/static/icons/* web/static/manifest.json web/static/sw.js web/templates/**
var WebFS embed.FS
