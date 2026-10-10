package web

import (
	_ "embed"
)

// IndexHTML contains the browser UI bundled into the executable.
//
//go:embed assets/index.html
var IndexHTML []byte
