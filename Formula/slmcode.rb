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
      sha256 "3716a52537c88c72b06e5af320cdc43d54b8fba2502eb237eed42d612e5189b8"
    end
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode-v#{version}-darwin-amd64.tar.gz"
      sha256 "41b692a0af21e0e1a9c5b73b1d7683aba483b92472f8012f2880db453970e538"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode-v#{version}-linux-amd64.tar.gz"
      sha256 "d0d25fb63c4e88d240a714c35bc86fa1ce0e788c4629909cd8f1177ca2584146"
    end
  end

  def install
    bin.install Dir["slmcode-v#{version}-*"].first => "slmcode"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/slmcode version")
  end
end
