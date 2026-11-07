package web

import "embed"

//go:embed index.html strings files
var Assets embed.FS
