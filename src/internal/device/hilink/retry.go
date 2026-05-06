package hilink

import "time"

const MaxAttempts = 2

var Backoffs = [...]time.Duration{200 * time.Millisecond, 600 * time.Millisecond}
