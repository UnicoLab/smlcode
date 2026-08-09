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
  version "0.10.1"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode-v#{version}-darwin-arm64.tar.gz"
      sha256 "992a815002527922b3469c1d3724ae5617e4bfa1243b0add4fce007b55320158"
    end
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode-v#{version}-darwin-amd64.tar.gz"
      sha256 "de3c326a73d38adab89feef5a6721e9166955952482c1f84b717a4e23dd4cc48"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode-v#{version}-linux-amd64.tar.gz"
      sha256 "a25ba8edc8df68c70ce46d51b50ebbffc47728b0d628c111d0f0a2aa58d8e4a2"
    end
  end

  def install
    bin.install Dir["slmcode-v#{version}-*"].first => "slmcode"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/slmcode version")
  end
end
