package web

import "embed"

//go:embed index.html app.js style.css photos.js photos.css
var Files embed.FS
