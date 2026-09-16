package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// memStore is the check state held in memory, standing in for git config.
type memStore struct {
	mu     sync.Mutex
	values map[string]string
}

func newStore(pairs ...string) *memStore {
	s := &memStore{values: map[string]string{}}
	for i := 0; i+1 < len(pairs); i += 2 {
		s.values[pairs[i]] = pairs[i+1]
	}
	return s
}

func (s *memStore) Get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key]
}

func (s *memStore) Bool(key string, def bool) bool {
	switch s.Get(key) {
	case "":
		return def
	case "false", "no", "off", "0":
		return false
	default:
		return true
	}
}

func (s *memStore) SetGlobal(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

// serving publishes one release the way GitHub does: /releases/latest
// redirects to the tag page, which is all Latest reads.
func serving(t *testing.T, tag string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/"+tag, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// check runs one complete check against a server publishing tag, and reports
// what the user would be offered.
func check(t *testing.T, current, tag string, store *memStore) (string, bool) {
	t.Helper()
	c := Start(context.Background(), Options{
		Current: current,
		Store:   store,
		BaseURL: serving(t, tag),
	})
	if c == nil {
		return "", false
	}
	return c.Available()
}

func TestOffersANewerRelease(t *testing.T) {
	store := newStore()
	tag, ok := check(t, "v1.2.0", "v1.3.0", store)
	if !ok || tag != "v1.3.0" {
		t.Fatalf("offered %q, %v; want v1.3.0, true", tag, ok)
	}
}

func TestSaysNothingWhenCurrentIsLatest(t *testing.T) {
	if _, ok := check(t, "v1.3.0", "v1.3.0", newStore()); ok {
		t.Fatal("offered an upgrade to the version already running")
	}
}

func TestSaysNothingWhenRunningAheadOfTheRelease(t *testing.T) {
	if _, ok := check(t, "v1.4.0", "v1.3.0", newStore()); ok {
		t.Fatal("offered a downgrade")
	}
}

func TestDeclinedVersionIsNotOfferedAgain(t *testing.T) {
	store := newStore(KeySkipVersion, "v1.3.0")
	if _, ok := check(t, "v1.2.0", "v1.3.0", store); ok {
		t.Fatal("offered a version the user had already declined")
	}
}

// The record is about one version bump, so the next release starts a new
// conversation rather than inheriting the answer to the last one.
func TestALaterReleaseIsOfferedAfterADecline(t *testing.T) {
	store := newStore(KeySkipVersion, "v1.3.0")
	tag, ok := check(t, "v1.2.0", "v1.4.0", store)
	if !ok || tag != "v1.4.0" {
		t.Fatalf("offered %q, %v; want v1.4.0, true", tag, ok)
	}
}

func TestDeclineRecordsOnlyThatVersion(t *testing.T) {
	store := newStore()
	c := Start(context.Background(), Options{
		Current: "v1.2.0", Store: store, BaseURL: serving(t, "v1.3.0"),
	})
	tag, ok := c.Available()
	if !ok {
		t.Fatal("no release was offered")
	}
	c.Decline(tag)
	if got := store.Get(KeySkipVersion); got != "v1.3.0" {
		t.Fatalf("recorded %q; want v1.3.0", got)
	}
}

// A build from source cannot be placed relative to a release, so stk does not
// guess -- and makes no request at all.
func TestSourceBuildsAreNeverChecked(t *testing.T) {
	for _, current := range []string{"dev", "v1.2.0-4-gdeadbee", ""} {
		store := newStore()
		if c := Start(context.Background(), Options{
			Current: current, Store: store, BaseURL: serving(t, "v9.9.9"),
		}); c != nil {
			t.Fatalf("%q: started a check for a build that is not a release", current)
		}
	}
}

func TestSuppressedRunsMakeNoRequest(t *testing.T) {
	if c := Start(context.Background(), Options{
		Current: "v1.2.0", Store: newStore(), Suppressed: true, BaseURL: serving(t, "v1.3.0"),
	}); c != nil {
		t.Fatal("started a check the caller had suppressed")
	}
}

func TestTheIntervalHoldsOffASecondCheck(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	recent := strconv.FormatInt(now.Add(-Interval/2).Unix(), 10)

	store := newStore(KeyLastCheck, recent)
	if c := Start(context.Background(), Options{
		Current: "v1.2.0", Store: store, BaseURL: serving(t, "v1.3.0"),
		Now: func() time.Time { return now },
	}); c != nil {
		t.Fatal("checked again inside the quiet period")
	}

	stale := strconv.FormatInt(now.Add(-Interval-time.Second).Unix(), 10)
	store = newStore(KeyLastCheck, stale)
	c := Start(context.Background(), Options{
		Current: "v1.2.0", Store: store, BaseURL: serving(t, "v1.3.0"),
		Now: func() time.Time { return now },
	})
	if c == nil {
		t.Fatal("did not check once the quiet period had elapsed")
	}
	if _, ok := c.Available(); !ok {
		t.Fatal("no release offered after the quiet period")
	}
	if got := store.Get(KeyLastCheck); got != strconv.FormatInt(now.Unix(), 10) {
		t.Fatalf("last check recorded as %q; want %d", got, now.Unix())
	}
}

// A clock that has moved backwards must not park the check in the future.
func TestATimestampInTheFutureStillChecks(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ahead := strconv.FormatInt(now.Add(72*time.Hour).Unix(), 10)
	store := newStore(KeyLastCheck, ahead)
	if c := Start(context.Background(), Options{
		Current: "v1.2.0", Store: store, BaseURL: serving(t, "v1.3.0"),
		Now: func() time.Time { return now },
	}); c == nil {
		t.Fatal("a timestamp in the future suppressed the check")
	}
}

// An unreachable server leaves no timestamp behind, so the next command tries
// again instead of waiting out the interval on a check that never happened.
func TestAFailedCheckIsSilentAndNotRecorded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	store := newStore()
	c := Start(context.Background(), Options{
		Current: "v1.2.0", Store: store, BaseURL: srv.URL,
	})
	if _, ok := c.Available(); ok {
		t.Fatal("offered a release the server never served")
	}
	if store.Get(KeyLastCheck) != "" {
		t.Fatal("recorded a check that failed")
	}
}

// The user has their answer; news about a version does not get to hold the
// terminal for it.
func TestASlowServerIsAbandoned(t *testing.T) {
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-unblock
		w.WriteHeader(http.StatusOK)
	}))
	defer func() { close(unblock); srv.Close() }()

	c := Start(context.Background(), Options{
		Current: "v1.2.0", Store: newStore(), BaseURL: srv.URL,
	})
	start := time.Now()
	if _, ok := c.Available(); ok {
		t.Fatal("offered a release from a request that never finished")
	}
	if waited := time.Since(start); waited > 3*Grace {
		t.Fatalf("waited %s for a slow server; the grace period is %s", waited, Grace)
	}
}

func TestANilCheckerReportsNothing(t *testing.T) {
	var c *Checker
	if _, ok := c.Available(); ok {
		t.Fatal("a checker that was never started offered a release")
	}
	c.Decline("v1.3.0") // must not panic
}

func TestEnabledHonoursBothSwitches(t *testing.T) {
	none := func(string) string { return "" }
	set := func(string) string { return "1" }

	if !Enabled(newStore(), none) {
		t.Fatal("checks are off by default; they should be on")
	}
	if Enabled(newStore(), set) {
		t.Fatalf("%s did not turn the check off", EnvDisable)
	}
	if Enabled(newStore(KeyEnabled, "false"), none) {
		t.Fatalf("%s = false did not turn the check off", KeyEnabled)
	}
	if !Enabled(newStore(KeyEnabled, "true"), none) {
		t.Fatalf("%s = true turned the check off", KeyEnabled)
	}
}

func TestNoticeSaysTheOfferIsNotRepeated(t *testing.T) {
	var joined string
	for _, line := range Notice("v1.3.0", "v1.2.0") {
		joined += line + "\n"
	}
	for _, want := range []string{"v1.3.0 is available", "you have v1.2.0", "will not mention v1.3.0 again", "stk upgrade"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the notice does not say %q:\n%s", want, joined)
		}
	}
}
