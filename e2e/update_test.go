package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/egmacke/stk/internal/release"
)

// runningVersion is the version the binary under test claims to be. The check
// says nothing for a build that is not a published release, so these tests
// need one that is.
const runningVersion = "v1.2.0"

// publishedVersion is what the fake release server offers.
const publishedVersion = "v9.9.9"

var (
	stampedOnce sync.Once
	stampedBin  string
	stampedErr  error
)

// stampedSTK builds an stk stamped with a release version, the way make dist
// does. The shared binary from TestMain reports "dev", which the check treats
// as a source build and ignores.
func stampedSTK(t *testing.T) string {
	t.Helper()
	stampedOnce.Do(func() {
		stampedBin = filepath.Join(filepath.Dir(stkBin), "stk-stamped")
		build := exec.Command("go", "build",
			"-ldflags", "-X main.version="+runningVersion,
			"-o", stampedBin, "..")
		build.Stdout = os.Stderr
		build.Stderr = os.Stderr
		stampedErr = build.Run()
	})
	if stampedErr != nil {
		t.Fatalf("building a stamped stk: %v", stampedErr)
	}
	return stampedBin
}

// releaseServer serves one published release exactly as GitHub does: the
// redirect the check reads, and the archive and checksums an upgrade verifies.
func releaseServer(t *testing.T, tag, payload string) string {
	t.Helper()
	platform, err := release.Platform()
	if err != nil {
		t.Skipf("no published builds for this platform: %v", err)
	}
	archive := tarball(t, payload)
	name := release.ArchiveName(tag, platform)
	sum := sha256.Sum256(archive)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)

	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/"+tag, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/releases/download/"+tag+"/"+name, func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	mux.HandleFunc("/releases/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, checksums)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func tarball(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{
		Name: release.BinaryName, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// watching points the repository's stk at a fake release server publishing
// tag, and returns a function that runs the stamped binary in it.
//
// The answer is piped in, so --interactive stands in for the terminal the
// check would otherwise require.
func watching(t *testing.T, tag string) (*repo, func(answer string, args ...string) result) {
	t.Helper()
	r := newRepo(t)
	r.extraEnv = append(r.extraEnv, "STK_UPDATE_BASE_URL="+releaseServer(t, tag, "new stk\n"))
	bin := stampedSTK(t)
	return r, func(answer string, args ...string) result {
		full := append([]string{"--no-color", "--interactive"}, args...)
		return r.runIn(r.Root, answer, bin, full...)
	}
}

// globalConfig reads a key from the test's own git configuration, where the
// check records what it has seen and what the user has declined.
func (r *repo) globalConfig(key string) string {
	r.t.Helper()
	res := r.runIn(r.Root, "", "git", "config", "--global", "--get", key)
	return strings.TrimRight(res.Stdout, "\n")
}

// forgetLastCheck clears the quiet period, so a test can run a second command
// without waiting an hour for it to be due again.
func (r *repo) forgetLastCheck() {
	r.t.Helper()
	r.runIn(r.Root, "", "git", "config", "--global", "--unset", "stk.lastUpdateCheck")
}

func TestUpdateNoticeOffersTheNewReleaseAndSaysItWillNotAskAgain(t *testing.T) {
	r, stk := watching(t, publishedVersion)

	res := stk("n\n", "stack")
	requireEqual(t, res.Code, 0, "exit code with an update available")
	out := res.All()
	requireContains(t, out, "stk "+publishedVersion+" is available (you have "+runningVersion+")")
	requireContains(t, out, "will not mention "+publishedVersion+" again")
	requireContains(t, out, "stk upgrade")
	requireContains(t, out, "Upgrade now?")

	requireEqual(t, r.globalConfig("stk.skipVersion"), publishedVersion, "the declined version")
}

// The whole point of recording the decline: the offer is made once per
// release, not once per quiet period.
func TestDeclinedVersionIsNotOfferedOnTheNextCommand(t *testing.T) {
	r, stk := watching(t, publishedVersion)

	stk("n\n", "stack")
	r.forgetLastCheck()

	out := stk("", "stack").All()
	requireNotContains(t, out, publishedVersion)
	requireNotContains(t, out, "Upgrade now?")
}

// A prompt the user walked away from is not a decision, so it is not written
// down and the offer stands.
func TestADismissedPromptIsNotRecordedAsADecline(t *testing.T) {
	r, stk := watching(t, publishedVersion)

	out := stk("", "stack").All()
	requireContains(t, out, "Upgrade now?")
	requireEqual(t, r.globalConfig("stk.skipVersion"), "", "skipVersion after a dismissed prompt")

	r.forgetLastCheck()
	requireContains(t, stk("", "stack").All(), publishedVersion+" is available")
}

func TestAcceptingTheOfferInstallsTheRelease(t *testing.T) {
	r := newRepo(t)
	payload := "the new stk\n"
	r.extraEnv = append(r.extraEnv, "STK_UPDATE_BASE_URL="+releaseServer(t, publishedVersion, payload))
	// Declining an earlier release must not stop this one being offered, and
	// must not survive the upgrade.
	r.runIn(r.Root, "", "git", "config", "--global", "stk.skipVersion", "v1.2.1")

	// Accepting replaces the running binary, so the test runs a copy of its
	// own rather than the one every other test shares.
	bin := filepath.Join(r.bin, "stk-upgrading")
	copyFile(t, stampedSTK(t), bin)

	res := r.runIn(r.Root, "y\n", bin, "--no-color", "--interactive", "stack")
	requireEqual(t, res.Code, 0, "exit code after accepting the offer")
	requireContains(t, res.All(), "Installed stk "+publishedVersion)

	installed, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	requireEqual(t, string(installed), payload, "the installed binary")
	requireEqual(t, r.globalConfig("stk.skipVersion"), "", "skipVersion after an upgrade")
}

func TestUpdateCheckStaysOutOfTheWay(t *testing.T) {
	cases := []struct {
		name string
		args []string
		env  []string
	}{
		{name: "quiet", args: []string{"--quiet", "stack"}},
		{name: "dry run", args: []string{"--dry-run", "stack"}},
		{name: "json", args: []string{"stack", "--json"}},
		{name: "version", args: []string{"version"}},
		{name: "env switch", args: []string{"stack"}, env: []string{"STK_NO_UPDATE_CHECK=1"}},
		{name: "CI", args: []string{"stack"}, env: []string{"CI=true"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, stk := watching(t, publishedVersion)
			r.extraEnv = append(r.extraEnv, tc.env...)
			out := stk("", tc.args...).All()
			requireNotContains(t, out, "Upgrade now?")
			requireNotContains(t, out, publishedVersion+" is available")
		})
	}
}

func TestUpdateCheckIsSilentWithoutSomeoneToAsk(t *testing.T) {
	r, _ := watching(t, publishedVersion)
	// No --interactive and no terminal: there is nobody to answer, so stk does
	// not ask the release server either.
	res := r.runIn(r.Root, "", stampedSTK(t), "--no-color", "stack")
	requireNotContains(t, res.All(), "Upgrade now?")
	requireEqual(t, r.globalConfig("stk.lastUpdateCheck"), "", "a check that should never have run")
}

func TestUpdateCheckCanBeTurnedOffInConfig(t *testing.T) {
	r, stk := watching(t, publishedVersion)
	r.runIn(r.Root, "", "git", "config", "--global", "stk.updateCheck", "false")
	requireNotContains(t, stk("", "stack").All(), "Upgrade now?")
}

// A failed command leaves the terminal to the error that caused it.
func TestNoOfferAfterAFailedCommand(t *testing.T) {
	_, stk := watching(t, publishedVersion)
	res := stk("", "checkout", "no-such-branch")
	if res.Code == 0 {
		t.Fatal("checking out a missing branch unexpectedly succeeded")
	}
	requireNotContains(t, res.All(), "Upgrade now?")
}

// Passthrough must stay a transparent git alias: git's output, git's exit
// code, and nothing of stk's appended to it.
func TestNoOfferOnAGitPassthrough(t *testing.T) {
	_, stk := watching(t, publishedVersion)
	out := stk("", "status", "--short").All()
	requireNotContains(t, out, "Upgrade now?")
	requireNotContains(t, out, publishedVersion)
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, body, 0o755); err != nil {
		t.Fatal(err)
	}
}
