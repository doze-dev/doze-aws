package asl

import (
	"math"
	"time"
)

// Retry and Catch: matching an error name against ErrorEquals lists, and
// computing backoff. The names are matched exactly — errors.go explains why a
// near-miss silently never matches, which is the bug this file exists to not
// have.

// matches reports whether an error name matches an ErrorEquals list.
// States.ALL matches everything; States.TaskFailed is Task's wildcard and
// matches any error except States.Timeout.
func matches(name string, errorEquals []string) bool {
	for _, e := range errorEquals {
		switch e {
		case ErrAll:
			return true
		case ErrTaskFailed:
			if name != ErrTimeout {
				return true
			}
		default:
			if e == name {
				return true
			}
		}
	}
	return false
}

// catchFor finds the first Catcher matching the failure, or nil.
func catchFor(s *State, fail *Failure) *Catcher {
	for _, c := range s.Catch {
		if matches(fail.Name, c.ErrorEquals) {
			return c
		}
	}
	return nil
}

// nextRetry decides the fate of a failure on a state with Retry: which
// retrier absorbs it and after what delay, or ok=false when no retrier will.
// Spec defaults: IntervalSeconds 1, MaxAttempts 3, BackoffRate 2.0. The
// attempt count read here is the retries already used, so the first retry
// waits exactly IntervalSeconds.
func nextRetry(s *State, f *Frame, fail *Failure, env Env) (retrierIdx int, delay time.Duration, ok bool) {
	for i, r := range s.Retry {
		if !matches(fail.Name, r.ErrorEquals) {
			continue
		}
		attempts := 0
		if i < len(f.Attempts) {
			attempts = f.Attempts[i]
		}
		maxAttempts := 3
		if r.MaxAttempts != nil {
			maxAttempts = *r.MaxAttempts
		}
		if attempts >= maxAttempts {
			return 0, 0, false // this retrier is exhausted; AWS does not fall through
		}
		interval := 1.0
		if r.IntervalSeconds != nil {
			interval = *r.IntervalSeconds
		}
		backoff := 2.0
		if r.BackoffRate != nil {
			backoff = *r.BackoffRate
		}
		secs := interval * math.Pow(backoff, float64(attempts))
		if r.MaxDelaySeconds != nil && secs > *r.MaxDelaySeconds {
			secs = *r.MaxDelaySeconds
		}
		if r.JitterStrategy == "FULL" && env.Rand != nil {
			secs *= env.Rand()
		}
		return i, time.Duration(secs * float64(time.Second)), true
	}
	return 0, 0, false
}
