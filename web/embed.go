package web

import "embed"

// Dist uses a sibling sentinel so fresh clones compile without tracking generated output.
//
//go:embed dist*
var Dist embed.FS
