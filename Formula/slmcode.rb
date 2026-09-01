# Homebrew formula for SLMCode.
#
# One-liner:
#   brew install --formula https://raw.githubusercontent.com/UnicoLab/smlcode/main/Formula/slmcode.rb
#
# Or tap this repo:
#   brew tap UnicoLab/smlcode https://github.com/UnicoLab/smlcode
#   brew install slmcode
class Slmcode < Formula
  desc "Coding harness for SLMs and any OpenAI-compatible LLM — building blocks, language packs, Studio UI"
  homepage "https://unicolab.ai"
  version "0.24.0"
  license "MIT"

  # ── About the sha256 values below ────────────────────────────────────────
  # They are written by scripts/update-formula.sh, which the release workflow
  # runs AFTER the binaries for this version are built and uploaded. There is a
  # window between the version bump landing on main and that sync completing in
  # which no real checksum can exist yet, because the binaries do not exist yet.
  #
  # In that window these are all-zero PLACEHOLDERS, on purpose. The obvious
  # alternative — leaving the previous release's real checksums in place — is
  # worse: `brew install` fails either way, but a stale-yet-plausible 64-hex
  # value produces a mismatch that is indistinguishable from a tampered
  # download, and the honest answer ("this formula has not been synced yet") is
  # unavailable to the person reading the error. Sixty-four zeros can only mean
  # one thing. scripts/check-version.sh enforces the shape and reports how many
  # are still placeholders.
  #
  # Once synced, each value carries a trailing "# v<version>" label naming the
  # release it was computed for, and that label must agree with the `version`
  # line above. It is there because a rebase resolves line by line: in v0.18.4
  # a release commit replayed onto the bot's v0.18.3 sync commit moved the
  # version line while leaving v0.18.3's digests underneath it, and nothing
  # noticed. A label on the digest's own line cannot be carried across a
  # release without carrying the contradiction with it, and
  # scripts/check-version.sh fails the build on exactly that.
  #
  # If you hit a mismatch against a zeroed value: the release workflow has not
  # finished. Install with the curl one-liner, or wait for the
  # "chore: sync Homebrew formula checksums" commit on main.
  on_macos do
    on_arm do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_darwin_arm64"
      sha256 "789e819562bef51fb0702d1933c51ddf3de2350b6b130cec49ff497f0742ac1d" # v0.24.0
    end
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_darwin_amd64"
      sha256 "3b9d19d7c24b3697d04502d500f45a6e9683145a1d9b1b3f3859dd54aeff3c91" # v0.24.0
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_linux_arm64"
      sha256 "2c2c1c580b2972590a62176e1ab6ce77fd1cfc27c4d41f0745ded5ce9ad28a74" # v0.24.0
    end
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_linux_amd64"
      sha256 "fa3c38d0f44bd56506cda0c5efffd442d3f5fe0fa5dd7edde150a7f278b4759a" # v0.24.0
    end
  end

  def install
    # The release asset is a bare binary, so Homebrew stages it under the URL's
    # basename (slmcode_<version>_<os>_<arch>). Exactly one such file exists in
    # the staging directory; `.first` on an empty glob would be a nil NoMethodError
    # with no explanation, so name the failure.
    staged = Dir["slmcode_*"].first
    odie "no slmcode_* binary in the downloaded asset — the release may be incomplete" if staged.nil?
    bin.install staged => "slmcode"
  end

  test do
    # Never let `brew test` reach the network: `slmcode version --check` and the
    # startup notice both query the GitHub release API, and a sandboxed or
    # offline test machine would fail on that rather than on the binary.
    ENV["SLMCODE_SKIP_UPDATE_CHECK"] = "1"

    # 1. The binary runs, and reports the version this formula claims to install.
    assert_match version.to_s, shell_output("#{bin}/slmcode version")

    # 2. The machine-readable form agrees — this is what catches a formula that
    #    installed the previous release's asset under the new version's name.
    require "json"
    info = JSON.parse(shell_output("#{bin}/slmcode version --json"))
    assert_equal version.to_s, info["version"]
    refute_empty info["commit"].to_s
    refute_equal "unknown", info["commit"].to_s

    # 3. The CLI actually works, rather than merely printing a version string:
    #    initialise a throwaway workspace and read the status back out. No model,
    #    no network and no provider are needed — `init` warns that nothing is
    #    listening on the endpoint and still exits 0.
    system bin/"slmcode", "init"
    assert_predicate testpath/".slmcode", :directory?
    assert_match "provider", shell_output("#{bin}/slmcode status")

    # 4. An unknown subcommand exits 2 rather than silently doing something.
    #    shell_output raises unless the status matches, so this is the assertion.
    shell_output("#{bin}/slmcode definitely-not-a-command 2>&1", 2)
  end
end
