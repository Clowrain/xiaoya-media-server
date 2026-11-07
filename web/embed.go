package web

import "embed"

//go:embed index.html strings files assets/*
var Assets embed.FS
