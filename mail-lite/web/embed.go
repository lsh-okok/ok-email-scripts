package web

import "embed"

// Assets includes the production admin application and the public mailbox page.
//
//go:embed all:dist templates
var Assets embed.FS
