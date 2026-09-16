// Package update tells the user, once, when a newer stk has been published.
//
// The check is a background HTTP request started as a command begins and read
// back after it ends, so it costs a command no time it would not otherwise
// spend. Nothing here can fail a command: every error is a reason to say
// nothing, never a reason to return one.
//
// A version the user declines is written down, so the offer is made once per
// release rather than once an hour. A newer release than the declined one is
// offered again: the record is about that version bump, not about the feature.
package update

import (
	"context"
	"strconv"
	"time"

	"github.com/egmacke/stk/internal/release"
)

const (
	// KeyEnabled turns the check off entirely when set to false. It is read
	// from any scope, so a user can disable it for themselves and a repository
	// can disable it for everyone working in it.
	KeyEnabled = "stk.updateCheck"
	// KeySkipVersion is the release the user has already said no to. It is
	// written to the user's own config, because it is a fact about this
	// machine's stk rather than about one repository.
	KeySkipVersion = "stk.skipVersion"
	// KeyLastCheck is when stk last reached the release server, as a Unix
	// timestamp.
	KeyLastCheck = "stk.lastUpdateCheck"

	// EnvDisable turns the check off for one invocation or one shell, for
	// people who would rather not edit their git config.
	EnvDisable = "STK_NO_UPDATE_CHECK"
	// EnvBaseURL points stk at another release server, for both the check and
	// the upgrade that may follow it. It exists so the end-to-end tests can
	// serve a release of their own.
	EnvBaseURL = "STK_UPDATE_BASE_URL"
)

const (
	// Interval is the quiet period between two checks on one machine.
	Interval = time.Hour
	// Grace is how long a finished command will wait for a check still in
	// flight. A user who has got their answer should not be kept waiting for
	// news about a version, so the request is abandoned rather than allowed to
	// hold the terminal.
	Grace = time.Second
	// timeout bounds the request itself, for the case where the command
	// outlives the grace period and the goroutine is still running.
	timeout = 10 * time.Second
)

// Store is the persistent state behind the check: when stk last looked, and
// which release the user has already turned down.
type Store interface {
	// Get reads a key from whichever scope defines it, returning "" when none
	// does.
	Get(key string) string
	// Bool reads a key as a git boolean, returning def when no scope defines
	// it.
	Bool(key string, def bool) bool
	// SetGlobal writes a key to the user's own configuration.
	SetGlobal(key, value string) error
}

// Checker runs one version check for one command.
//
// The zero value is not usable; call Start, which returns nil when the check
// is not wanted, and treat a nil Checker as one that reports nothing.
type Checker struct {
	store   Store
	current string

	done   chan struct{}
	newer  string // the published tag, when it is newer than current
	source release.Source
	now    func() time.Time
}

// Options are the facts a caller has already established about this run.
type Options struct {
	// Current is the running build's version.
	Current string
	// Store persists the check state.
	Store Store
	// Suppressed says the caller has already decided not to check: the
	// command cannot prompt, or was asked to be quiet. The reason is the
	// caller's business; the effect here is that nothing is checked and no
	// request is made.
	Suppressed bool
	// BaseURL overrides the release server. Empty means github.com.
	BaseURL string
	// Now overrides the clock, for tests.
	Now func() time.Time
}

// Start begins a check in the background, or returns nil when there is nothing
// worth checking.
//
// It returns before the request is made, so the command it belongs to starts
// no later than it otherwise would.
func Start(ctx context.Context, opts Options) *Checker {
	if opts.Suppressed || opts.Store == nil {
		return nil
	}
	// A build that is not a published release cannot be compared with one, so
	// there is no "newer" to report and nothing useful to say.
	if !release.IsReleaseTag(opts.Current) {
		return nil
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	c := &Checker{
		store:   opts.Store,
		current: opts.Current,
		done:    make(chan struct{}),
		source:  release.Source{BaseURL: opts.BaseURL},
		now:     now,
	}
	if !c.due() {
		return nil
	}
	go c.run(ctx)
	return c
}

// due reports whether the quiet period since the last check has elapsed.
//
// An unreadable or absent timestamp counts as due: a machine that has never
// checked, or whose record has been edited by hand, should check.
func (c *Checker) due() bool {
	raw := c.store.Get(KeyLastCheck)
	if raw == "" {
		return true
	}
	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return true
	}
	last := time.Unix(secs, 0)
	// A timestamp in the future means a clock that has moved backwards since
	// it was written. Checking now is the recoverable answer; waiting would
	// suppress the check until the future caught up.
	if last.After(c.now()) {
		return true
	}
	return c.now().Sub(last) >= Interval
}

func (c *Checker) run(ctx context.Context) {
	defer close(c.done)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tag, err := c.source.Latest(ctx)
	if err != nil {
		// An unreachable release server is not the user's problem right now.
		// The timestamp is deliberately not written, so the next command tries
		// again rather than waiting out the interval.
		return
	}
	// Recorded as soon as the answer arrives rather than when it is reported,
	// so a command that finishes before the grace period still counts as
	// having checked.
	_ = c.store.SetGlobal(KeyLastCheck, strconv.FormatInt(c.now().Unix(), 10))

	order, ok := release.Compare(c.current, tag)
	if !ok || order >= 0 {
		return
	}
	// A version already turned down is not offered again. It is read here
	// rather than before the request so that declining one release still
	// leaves stk noticing the next.
	if c.store.Get(KeySkipVersion) == tag {
		return
	}
	c.newer = tag
}

// Available returns the newer release to offer, waiting up to Grace for a
// check still in flight.
//
// ok is false when there is nothing to say: no newer release, one the user has
// already declined, or an answer that did not arrive in time.
func (c *Checker) Available() (tag string, ok bool) {
	if c == nil {
		return "", false
	}
	select {
	case <-c.done:
	case <-time.After(Grace):
		return "", false
	}
	return c.newer, c.newer != ""
}

// Decline records that the user does not want this release, so it is never
// offered again.
//
// Only the version is written down. A later release is a different offer, and
// stk will make it.
func (c *Checker) Decline(tag string) {
	if c == nil {
		return
	}
	_ = c.store.SetGlobal(KeySkipVersion, tag)
}

// Notice is what stk says when a newer release is found.
//
// It states the consequence of saying no, because the offer is not repeated:
// a user who declines without being told would have no way of knowing the
// prompt was their only one.
func Notice(tag, current string) []string {
	return []string{
		"stk " + tag + " is available (you have " + current + ").",
		"",
		"If you skip this, stk will not mention " + tag + " again.",
		"You can upgrade at any time by running:",
		"",
		"    stk upgrade",
		"",
	}
}
