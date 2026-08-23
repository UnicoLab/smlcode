package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/installmeta"
	"github.com/UnicoLab/slmcode/pkg/updatecheck"
)

// Binary self-update, without piping the internet into a shell.
//
// The old path was `curl -fsSL <url> | bash`, with the org/repo read from a
// world-readable config file: anyone who could write ~/.config/slmcode/install.json
// chose which script ran as the user. This downloads the release asset itself,
// verifies it against the release SHA256SUMS, replaces the binary atomically,
// and never touches a repo outside the allowlist.

// allowedRepos is the set of upstreams a binary update may be fetched from.
var allowedRepos = map[string]bool{
	"UnicoLab/smlcode": true,
	"UnicoLab/slmcode": true,
}

const (
	updateDefaultRepo = "UnicoLab/smlcode"
	updateHTTPTimeout = 120 * time.Second
	maxAssetBytes     = 256 << 20 // 256 MiB
)

// resolveUpdateRepo validates the configured repo against the allowlist.
func resolveUpdateRepo(meta *installmeta.Meta) (string, error) {
	repo := updateDefaultRepo
	if meta != nil && strings.TrimSpace(meta.Repo) != "" {
		repo = strings.TrimSpace(meta.Repo)
	}
	if env := strings.TrimSpace(os.Getenv("SLMCODE_UPDATE_REPO")); env != "" {
		repo = env
	}
	if !allowedRepos[repo] {
		return "", failf(2, "refusing to update from %q — not an allowed upstream (%s)",
			repo, strings.Join(sortedKeys(allowedRepos), ", "))
	}
	return repo, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Map iteration order is randomized, so without this the "allowed upstreams"
	// list in the refusal message came out in a different order every run — which
	// makes the one error a user is most likely to paste into an issue look like
	// two different errors.
	sort.Strings(out)
	return out
}

// releaseAsset describes one downloadable file on a GitHub release.
type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type releaseInfo struct {
	TagName string         `json:"tag_name"`
	HTMLURL string         `json:"html_url"`
	Assets  []releaseAsset `json:"assets"`
}

// assetName builds the platform asset name used by the release workflow:
// slmcode_<version>_<os>_<arch>, plus ".exe" on Windows.
//
// The .exe suffix is not cosmetic. .github/workflows/release.yml builds the
// Windows artifacts as slmcode_<version>_windows_<arch>.exe, so a name without
// it matches no asset on any release and `slmcode update` failed on Windows with
// "release vX.Y.Z has no asset ..." for every version — the one platform where
// re-running the installer by hand is the most awkward.
func assetName(version string) string {
	version = strings.TrimPrefix(strings.TrimPrefix(version, "v"), "V")
	name := fmt.Sprintf("slmcode_%s_%s_%s", version, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func fetchLatestRelease(repo string) (releaseInfo, error) {
	var rel releaseInfo
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return rel, fmt.Errorf("release lookup failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return rel, fmt.Errorf("release lookup returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return rel, fmt.Errorf("release lookup returned malformed JSON: %w", err)
	}
	return rel, nil
}

func findAsset(rel releaseInfo, name string) (releaseAsset, bool) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return releaseAsset{}, false
}

// downloadTo streams url into a temp file next to dstDir and returns its path
// plus the sha256 of the bytes written.
func downloadTo(url, dstDir, pattern string) (path, sum string, err error) {
	client := &http.Client{Timeout: updateHTTPTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download returned HTTP %d for %s", resp.StatusCode, url)
	}
	f, err := os.CreateTemp(dstDir, pattern)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxAssetBytes)); err != nil {
		removeStale(f.Name())
		return "", "", err
	}
	return f.Name(), hex.EncodeToString(h.Sum(nil)), nil
}

// parseSHA256SUMS maps file name → expected hex digest.
func parseSHA256SUMS(body string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		out[filepath.Base(name)] = strings.ToLower(fields[0])
	}
	return out
}

// updateFromBinary downloads, verifies and installs the latest release binary.
func updateFromBinary(meta *installmeta.Meta, checkOnly, userMode, system, assumeYes bool) error {
	repo, err := resolveUpdateRepo(meta)
	if err != nil {
		return err
	}
	cli.KeyVal("repo", repo)

	rel, err := fetchLatestRelease(repo)
	if err != nil {
		return failf(1, "%s", err.Error())
	}
	latest := strings.TrimPrefix(strings.TrimPrefix(rel.TagName, "v"), "V")
	cli.KeyVal("latest", latest)

	// Compare as versions, not as strings. String equality answers "are these
	// the same release?" but not "is that one newer?", so a plain `!=` treated
	// an OLDER published release — a re-tag, a rollback, a pre-release promoted
	// to latest — as an available update and would happily downgrade the user.
	cmp := updatecheck.CompareVersions(latest, Version)

	if checkOnly {
		switch {
		case cmp > 0:
			fmt.Println(cli.Warn("v" + latest + " is available — run: slmcode update"))
		case cmp < 0:
			fmt.Println(cli.Dim("this binary (v" + Version + ") is newer than the latest release (v" + latest + ")"))
		default:
			fmt.Println(cli.Success("installed binary matches the latest release"))
		}
		return nil
	}
	if cmp <= 0 {
		fmt.Println(cli.Success("already on the latest release — nothing to do"))
		if cmp < 0 {
			fmt.Println(cli.Dim("(this binary is v" + Version + "; the latest release is v" + latest + ")"))
		}
		return nil
	}

	name := assetName(latest)
	asset, ok := findAsset(rel, name)
	if !ok {
		return failf(1, "release %s has no asset %q for %s/%s — install manually from %s",
			rel.TagName, name, runtime.GOOS, runtime.GOARCH, rel.HTMLURL)
	}
	sums, ok := findAsset(rel, "SHA256SUMS")
	if !ok {
		return failf(1, "release %s publishes no SHA256SUMS — refusing to install an unverified binary", rel.TagName)
	}

	target, err := resolveUpdateTarget(userMode, system)
	if err != nil {
		return err
	}
	dstDir := filepath.Dir(target)

	if !assumeYes {
		fmt.Println()
		cli.KeyVal("asset", name)
		cli.KeyVal("install to", target)
		if !confirm(fmt.Sprintf("Download v%s and replace this binary?", latest), false) {
			fmt.Println(cli.Dim("canceled"))
			return nil
		}
	}

	// Verify the checksum list first, then the payload against it.
	sumPath, _, err := downloadTo(sums.URL, os.TempDir(), "slmcode-sums-*")
	if err != nil {
		return failf(1, "downloading SHA256SUMS: %s", err.Error())
	}
	defer removeStale(sumPath)
	sumBody, err := os.ReadFile(sumPath)
	if err != nil {
		return err
	}
	expected, ok := parseSHA256SUMS(string(sumBody))[name]
	if !ok || expected == "" {
		return failf(1, "%s is not listed in SHA256SUMS — refusing to install", name)
	}

	fmt.Println(cli.Info("downloading " + name + "…"))
	binPath, got, err := downloadTo(asset.URL, dstDir, "slmcode-new-*")
	if err != nil {
		// Fall back to the system temp dir when the install dir is not writable.
		binPath, got, err = downloadTo(asset.URL, os.TempDir(), "slmcode-new-*")
		if err != nil {
			return failf(1, "downloading %s: %s", name, err.Error())
		}
	}
	defer removeStale(binPath)

	if got != expected {
		return failf(1, "checksum mismatch for %s\n  expected %s\n  got      %s", name, expected, got)
	}
	fmt.Println(cli.Success("checksum verified"))

	// The downloaded release asset is the replacement executable; it needs +x.
	if err := os.Chmod(binPath, 0o755); err != nil { //nolint:gosec // must be executable: this is the replacement slmcode binary
		return err
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil { //nolint:gosec // a bin directory on PATH
		return failf(1, "creating %s: %s", dstDir, err.Error())
	}
	if err := atomicReplace(binPath, target); err != nil {
		return failf(1, "installing to %s: %s (try sudo, or --user to install into ~/.local/bin)", target, err.Error())
	}

	mode := "user"
	if meta != nil && meta.Mode != "" {
		mode = meta.Mode
	}
	if userMode {
		mode = "user"
	}
	if system {
		mode = "system"
	}
	_ = installmeta.Save(&installmeta.Meta{
		Prefix:      filepath.Dir(target),
		Mode:        mode,
		Method:      "binary",
		Version:     latest,
		Binary:      target,
		Repo:        repo,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	})

	fmt.Println(cli.Success("updated to v" + latest))
	fmt.Println(cli.Dim("verify: slmcode version && slmcode doctor"))
	return nil
}

// resolveUpdateTarget decides which file this update writes.
//
// By default that is the running binary, wherever it happens to live. --user
// and --system used to be accepted and then ignored on this path: they only
// changed the "mode" recorded in install.json, while the failure message told
// the user to "try --user to install into ~/.local/bin" — advice the flag did
// not implement. Now it does.
func resolveUpdateTarget(userMode, system bool) (string, error) {
	if userMode && system {
		return "", failf(2, "--user and --system are mutually exclusive")
	}
	if userMode {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", failf(1, "cannot resolve your home directory for --user: %s", err.Error())
		}
		name := "slmcode"
		if runtime.GOOS == "windows" {
			name = "slmcode.exe"
		}
		return filepath.Join(home, ".local", "bin", name), nil
	}
	// --system, and the default, both replace the binary that is running: it is
	// already at whichever prefix the user installed to, and replacing anything
	// else would leave two copies and a PATH coin-flip over which one wins.
	running := resolveBinaryPath()
	if running == "(unknown)" {
		return "", failf(1, "cannot locate the running binary to replace — reinstall with the one-liner in docs/install.md")
	}
	return running, nil
}

// atomicReplace moves src over dst, falling back to a copy when the two live on
// different filesystems. The running binary keeps executing from its open inode.
func atomicReplace(src, dst string) error {
	// Windows refuses to rename over or unlink a file that is mapped as a
	// running image, so both the rename and the copy fallback below fail with a
	// sharing violation when slmcode updates itself. Moving the running image
	// out of the way first is allowed, and the displaced file can be deleted on
	// the next run. POSIX needs none of this: there, rename(2) over a running
	// binary succeeds and the process keeps executing from its open inode.
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(dst); err == nil {
			old := dst + ".old"
			_ = os.Remove(old) // a leftover from a previous update; ignore if held
			if err := os.Rename(dst, old); err != nil {
				return fmt.Errorf("moving the running binary aside: %w", err)
			}
			if err := os.Rename(src, dst); err != nil {
				// Put it back rather than leaving the user with no slmcode at all.
				_ = os.Rename(old, dst)
				return err
			}
			_ = os.Remove(old)
			return nil
		}
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }() // read-only descriptor; nothing actionable on close error
	tmp := dst + ".new"
	// The output is the replacement executable itself, so it must carry +x;
	// 0600-or-less would make the installed binary unrunnable.
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec // must be executable: this is the replacement slmcode binary
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close() // best effort; the copy error above is what we report
		removeStale(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		removeStale(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		removeStale(tmp)
		return err
	}
	return nil
}

// removeStale best-effort removes a leftover temp file on an error path; a
// failure here is not actionable (the original error already dominates) but
// is worth a warning since it can leave debris behind.
func removeStale(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warning: failed to remove temp file %s: %v\n", path, err)
	}
}
