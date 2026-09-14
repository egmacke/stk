package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// archiveFor builds a release archive holding a stk binary with the given
// contents, exactly as make dist would.
func archiveFor(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{
		Name: BinaryName, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
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

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// fakeRelease serves one published release the way GitHub does, including the
// redirect from /releases/latest that Latest reads.
func fakeRelease(t *testing.T, tag, platform string, archive, sums []byte) *httptest.Server {
	t.Helper()
	name := ArchiveName(tag, platform)
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
		if sums == nil {
			http.NotFound(w, r)
			return
		}
		w.Write(sums)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchVerifiesAndUnpacks(t *testing.T) {
	const tag, platform = "v1.2.3", "linux_amd64"
	archive := archiveFor(t, "#!/bin/sh\necho stk\n")
	sums := []byte(fmt.Sprintf("%s  %s\n", digest(archive), ArchiveName(tag, platform)))

	srv := fakeRelease(t, tag, platform, archive, sums)
	src := Source{BaseURL: srv.URL}

	dir := t.TempDir()
	path, err := src.Fetch(context.Background(), tag, platform, dir)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "#!/bin/sh\necho stk\n" {
		t.Errorf("unpacked %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("unpacked binary is not executable: %v", info.Mode())
	}
}

// A tampered archive must not reach the disk at all, not merely be reported.
func TestFetchRejectsTamperedArchive(t *testing.T) {
	const tag, platform = "v1.2.3", "linux_amd64"
	archive := archiveFor(t, "genuine")
	sums := []byte(fmt.Sprintf("%s  %s\n", digest(archive), ArchiveName(tag, platform)))

	srv := fakeRelease(t, tag, platform, append(archive, "evil"...), sums)
	src := Source{BaseURL: srv.URL}

	dir := t.TempDir()
	_, err := src.Fetch(context.Background(), tag, platform, dir)
	if err == nil {
		t.Fatal("expected a checksum failure")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error was %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("tampered download left %d file(s) behind", len(entries))
	}
}

// Without checksums there is nothing to verify against, so stk must refuse
// rather than fall back to installing whatever arrived.
func TestFetchRefusesWithoutChecksums(t *testing.T) {
	const tag, platform = "v1.2.3", "linux_amd64"
	archive := archiveFor(t, "genuine")

	srv := fakeRelease(t, tag, platform, archive, nil)
	src := Source{BaseURL: srv.URL}

	_, err := src.Fetch(context.Background(), tag, platform, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unverified") {
		t.Fatalf("expected a refusal, got %v", err)
	}
}

func TestVerify(t *testing.T) {
	archive := []byte("payload")
	name := "stk_v1.0.0_linux_amd64.tar.gz"

	t.Run("matches", func(t *testing.T) {
		sums := []byte(digest(archive) + "  " + name + "\n")
		if err := Verify(archive, sums, name); err != nil {
			t.Errorf("Verify: %v", err)
		}
	})
	t.Run("binary mode star", func(t *testing.T) {
		sums := []byte(digest(archive) + " *" + name + "\n")
		if err := Verify(archive, sums, name); err != nil {
			t.Errorf("Verify: %v", err)
		}
	})
	t.Run("picks its own line", func(t *testing.T) {
		sums := []byte("0000  stk_v1.0.0_darwin_arm64.tar.gz\n" + digest(archive) + "  " + name + "\n")
		if err := Verify(archive, sums, name); err != nil {
			t.Errorf("Verify: %v", err)
		}
	})
	t.Run("mismatch", func(t *testing.T) {
		sums := []byte(strings.Repeat("a", 64) + "  " + name + "\n")
		if err := Verify(archive, sums, name); err == nil {
			t.Error("expected a mismatch")
		}
	})
	t.Run("not listed", func(t *testing.T) {
		sums := []byte(digest(archive) + "  something_else.tar.gz\n")
		err := Verify(archive, sums, name)
		if err == nil || !strings.Contains(err.Error(), "not listed") {
			t.Errorf("expected a not-listed refusal, got %v", err)
		}
	})
}

func TestLatest(t *testing.T) {
	srv := fakeRelease(t, "v2.0.1", "linux_amd64", nil, nil)
	got, err := Source{BaseURL: srv.URL}.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got != "v2.0.1" {
		t.Errorf("Latest = %q, want v2.0.1", got)
	}
}

func TestLatestWithNoReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(http.NotFound))
	t.Cleanup(srv.Close)
	_, err := Source{BaseURL: srv.URL}.Latest(context.Background())
	if err != ErrNoRelease {
		t.Errorf("err = %v, want ErrNoRelease", err)
	}
}

func TestArchiveName(t *testing.T) {
	if got := ArchiveName("v0.1.0", "darwin_arm64"); got != "stk_v0.1.0_darwin_arm64.tar.gz" {
		t.Errorf("ArchiveName = %q", got)
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b  string
		order int
		ok    bool
	}{
		{"v1.0.0", "v1.0.0", 0, true},
		{"v1.0.0", "v1.0.1", -1, true},
		{"v1.2.0", "v1.10.0", -1, true},
		{"v2.0.0", "v1.9.9", 1, true},
		{"0.1.0", "v0.1.0", 0, true},
		// A source build cannot be ordered against a release.
		{"dev", "v1.0.0", 0, false},
		{"v0.1.0-3-gabc123", "v0.1.0", 0, false},
	}
	for _, c := range cases {
		order, ok := Compare(c.a, c.b)
		if order != c.order || ok != c.ok {
			t.Errorf("Compare(%q, %q) = %d, %v; want %d, %v", c.a, c.b, order, ok, c.order, c.ok)
		}
	}
}

// Replace must swap the file in one step: a reader either sees the old binary
// or the new one, never a partial write, and no staging file is left behind.
func TestReplace(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "stk")
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "stk")
	if err := os.WriteFile(src, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Replace(dst, src); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("contents = %q, want new", got)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("Replace left %d entries, want 1", len(entries))
	}
}

func TestReplaceIntoUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, "stk")
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	src := filepath.Join(t.TempDir(), "stk")
	if err := os.WriteFile(src, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Replace(dst, src)
	if err == nil {
		t.Fatal("expected a permission failure")
	}
	// The message has to tell the user what to do about it.
	if !strings.Contains(err.Error(), "STK_INSTALL_DIR") {
		t.Errorf("error does not suggest a fix: %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "old" {
		t.Errorf("failed upgrade changed the binary to %q", got)
	}
}

func TestUnpackWithoutBinary(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	tw.WriteHeader(&tar.Header{Name: "README", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	tw.Write([]byte("hi"))
	tw.Close()
	zw.Close()

	_, err := unpack(buf.Bytes(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "did not contain") {
		t.Errorf("expected a missing-binary error, got %v", err)
	}
}
