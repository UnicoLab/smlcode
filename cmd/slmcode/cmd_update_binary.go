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
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/installmeta"
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
// slmcode_<version>_<os>_<arch>.
func assetName(version string) string {
	version = strings.TrimPrefix(strings.TrimPrefix(version, "v"), "V")
	return fmt.Sprintf("slmcode_%s_%s_%s", version, runtime.GOOS, runtime.GOARCH)
}

func fetchLatestRelease(repo string) (releaseInfo, error) {
	var rel releaseInfo
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return rel, fmt.Errorf("release lookup failed: %w", err)
	}
	defer resp.Body.Close()
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download returned HTTP %d for %s", resp.StatusCode, url)
	}
	f, err := os.CreateTemp(dstDir, pattern)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxAssetBytes)); err != nil {
		os.Remove(f.Name())
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

	if checkOnly {
		if latest == Version {
			fmt.Println(cli.Success("installed binary matches the latest release"))
		} else {
			fmt.Println(cli.Warn("v" + latest + " is available — run: slmcode update"))
		}
		return nil
	}
	if latest == Version {
		fmt.Println(cli.Success("already on the latest release — nothing to do"))
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

	target := resolveBinaryPath()
	if target == "(unknown)" {
		return failf(1, "cannot locate the running binary to replace")
	}
	dstDir := filepath.Dir(target)

	if !assumeYes {
		fmt.Println()
		cli.KeyVal("asset", name)
		cli.KeyVal("install to", target)
		if !confirm(fmt.Sprintf("Download v%s and replace this binary?", latest), false) {
			fmt.Println(cli.Dim("cancelled"))
			return nil
		}
	}

	// Verify the checksum list first, then the payload against it.
	sumPath, _, err := downloadTo(sums.URL, os.TempDir(), "slmcode-sums-*")
	if err != nil {
		return failf(1, "downloading SHA256SUMS: %s", err.Error())
	}
	defer os.Remove(sumPath)
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
	defer os.Remove(binPath)

	if got != expected {
		return failf(1, "checksum mismatch for %s\n  expected %s\n  got      %s", name, expected, got)
	}
	fmt.Println(cli.Success("checksum verified"))

	if err := os.Chmod(binPath, 0o755); err != nil {
		return err
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

// atomicReplace moves src over dst, falling back to a copy when the two live on
// different filesystems. The running binary keeps executing from its open inode.
func atomicReplace(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
