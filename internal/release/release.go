// Package release finds, verifies and installs published stk builds.
//
// It speaks exactly the protocol install.sh does: the same archive names, the
// same checksums.txt, the same GitHub release layout. The two are independent
// implementations of one published interface, so a binary installed by either
// can be upgraded by either.
//
// Nothing here trusts a download. An archive that is not listed in the
// release's own checksums.txt, or whose digest does not match it, is discarded
// rather than installed.
package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository releases are published to.
const Repo = "egmacke/stk"

// BinaryName is the file the release archives contain.
const BinaryName = "stk"

// maxArchive caps what stk will read from the network, so a wrong or hostile
// URL cannot fill the disk.
const maxArchive = 64 << 20

// ErrNoRelease reports that the repository has no published release yet.
var ErrNoRelease = errors.New("no published release found")

// client is deliberately short-lived: an upgrade that cannot make progress
// should fail while the user is still watching.
var client = &http.Client{Timeout: 60 * time.Second}

// Platform returns the archive infix for the running build, such as
// "linux_amd64".
//
// Only the platforms the release workflow actually builds are accepted; on
// anything else stk says so rather than fetching a 404.
func Platform() (string, error) {
	switch runtime.GOOS {
	case "linux", "darwin":
	default:
		return "", fmt.Errorf("stk ships prebuilt binaries for linux and darwin only; on %s build from source with 'make install'", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("stk ships prebuilt binaries for amd64 and arm64 only; on %s build from source with 'make install'", runtime.GOARCH)
	}
	return runtime.GOOS + "_" + runtime.GOARCH, nil
}

// ArchiveName is the release asset holding the binary for a platform.
func ArchiveName(tag, platform string) string {
	return fmt.Sprintf("%s_%s_%s.tar.gz", BinaryName, tag, platform)
}

// Source locates published releases. The zero value reads from the real
// repository on github.com.
type Source struct {
	// BaseURL overrides https://github.com/<Repo>, for tests.
	BaseURL string
}

func (s Source) base() string {
	if s.BaseURL != "" {
		return strings.TrimSuffix(s.BaseURL, "/")
	}
	return "https://github.com/" + Repo
}

// Latest resolves the tag of the most recent published release.
//
// It reads the redirect on /releases/latest rather than the REST API, which
// rate-limits unauthenticated callers to sixty requests an hour and would fail
// for everyone behind a shared address.
func (s Source) Latest(ctx context.Context) (string, error) {
	url := s.base() + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "stk-upgrade")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNoRelease
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned %s", url, resp.Status)
	}
	// The redirect lands on /releases/tag/<tag>; without one there is nothing
	// published to land on.
	final := resp.Request.URL.Path
	dir, tag := path.Split(final)
	if !strings.HasSuffix(dir, "/releases/tag/") || tag == "" {
		return "", ErrNoRelease
	}
	return tag, nil
}

// Fetch downloads the archive for tag on platform, checks it against the
// release's checksums.txt and unpacks the binary into dir.
//
// It returns the path of the verified binary.
func (s Source) Fetch(ctx context.Context, tag, platform, dir string) (string, error) {
	name := ArchiveName(tag, platform)
	base := fmt.Sprintf("%s/releases/download/%s", s.base(), tag)

	archive, err := s.get(ctx, base+"/"+name)
	if err != nil {
		return "", fmt.Errorf("could not download %s: %w", name, err)
	}
	sums, err := s.get(ctx, base+"/checksums.txt")
	if err != nil {
		return "", fmt.Errorf("could not download the checksums for %s (refusing to install an unverified binary): %w", tag, err)
	}
	if err := Verify(archive, sums, name); err != nil {
		return "", err
	}
	return unpack(archive, dir)
}

func (s Source) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "stk-upgrade")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxArchive))
}

// Verify checks archive against the "<digest>  <name>" line for name in a
// checksums.txt document.
//
// An archive the file does not mention is as bad as one whose digest is wrong:
// both mean stk cannot vouch for the bytes.
func Verify(archive, checksums []byte, name string) error {
	want := ""
	for _, line := range strings.Split(string(checksums), "\n") {
		digest, file, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		// Coreutils writes two spaces, and a leading "*" for binary mode.
		if strings.TrimPrefix(strings.TrimSpace(file), "*") == name {
			want = digest
			break
		}
	}
	if want == "" {
		return fmt.Errorf("%s is not listed in checksums.txt; refusing to install", name)
	}
	sum := sha256.Sum256(archive)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(want, got) {
		return fmt.Errorf("checksum mismatch for %s\n  expected %s\n  actual   %s\nThe download is corrupt or has been tampered with. Nothing was installed", name, want, got)
	}
	return nil
}

// unpack writes the binary held in a verified archive into dir.
func unpack(archive []byte, dir string) (string, error) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return "", fmt.Errorf("could not read the archive: %w", err)
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("could not read the archive: %w", err)
		}
		// Only the one known name is extracted, so a crafted archive cannot
		// write anywhere but the destination stk chose.
		if h.Typeflag != tar.TypeReg || filepath.Base(h.Name) != BinaryName {
			continue
		}
		dst := filepath.Join(dir, BinaryName)
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(f, io.LimitReader(tr, maxArchive)); err != nil {
			f.Close()
			return "", err
		}
		if err := f.Close(); err != nil {
			return "", err
		}
		return dst, nil
	}
	return "", fmt.Errorf("the archive did not contain a %s binary", BinaryName)
}

// Target is the path stk would replace when upgrading itself, with any
// symlinks resolved so the real file is written rather than the link.
func Target() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe, nil
	}
	return resolved, nil
}

// Replace moves src over dst.
//
// The new binary is staged beside dst and renamed into place, so an interrupted
// upgrade can never leave a half-written stk on PATH, and so replacing the
// running executable works: on Unix a rename detaches the old inode, which the
// running process goes on using until it exits.
func Replace(dst, src string) error {
	dir := filepath.Dir(dst)
	staged := filepath.Join(dir, "."+filepath.Base(dst)+".new")
	_ = os.Remove(staged)

	if err := copyFile(src, staged); err != nil {
		return fmt.Errorf("could not write to %s: %w\n\nIf stk was installed somewhere you do not own, reinstall it somewhere you do:\n\n    STK_INSTALL_DIR=$HOME/.local/bin sh install.sh", dir, err)
	}
	if err := os.Chmod(staged, 0o755); err != nil {
		os.Remove(staged)
		return err
	}
	if err := os.Rename(staged, dst); err != nil {
		os.Remove(staged)
		return fmt.Errorf("could not install to %s: %w", dst, err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Compare orders two version tags, returning -1, 0 or 1.
//
// ok is false when either side is not a release tag -- "dev" from a source
// build, or a git describe string from an untagged commit -- in which case the
// caller cannot reason about which is newer and should just install the target.
func Compare(a, b string) (order int, ok bool) {
	av, aok := parse(a)
	bv, bok := parse(b)
	if !aok || !bok {
		return 0, false
	}
	for i := range av {
		if av[i] != bv[i] {
			if av[i] < bv[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

// parse reads a vX.Y.Z tag. Anything carrying a prerelease or build suffix is
// rejected rather than guessed at, since ordering those correctly needs the
// full semver rules and stk does not publish them.
func parse(v string) ([3]int, bool) {
	var out [3]int
	s := strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
