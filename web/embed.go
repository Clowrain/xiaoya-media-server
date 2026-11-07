package web

import "embed"

//go:embed index.html strings assets/*
var Assets embed.FS
