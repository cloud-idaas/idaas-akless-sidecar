package credentials

import "time"

// Clock supports deterministic tests of credential expiry and retry windows.
type Clock interface {
	Now() time.Time
}

// SystemClock supplies wall-clock time to services created by NewService.
type SystemClock struct{}

func (c *SystemClock) Now() time.Time { return time.Now() }
