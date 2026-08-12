package clock

import "time"

type Clock interface {
	Now() time.Time
}

type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }

type Fixed struct{ Time time.Time }

func (clock Fixed) Now() time.Time { return clock.Time.UTC() }
