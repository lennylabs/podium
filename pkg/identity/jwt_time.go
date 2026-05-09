package identity

import "time"

// jwtTime is a thin alias used by the JWTVerifier's clock-injection
// path to keep the time stdlib confined to one file.
type jwtTime = time.Time
