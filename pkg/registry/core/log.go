package core

import "math"

// logImpl returns the natural log; isolated in its own file so the
// only stdlib math import lives here.
func logImpl(x float64) float64 { return math.Log(x) }
