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
  version "0.10.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode-v#{version}-darwin-arm64.tar.gz"
      sha256 "3ce026ca0ae18b8e91d6255c03702bce405aa13e7007cad0c80e3236d9e3af02"
    end
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode-v#{version}-darwin-amd64.tar.gz"
      sha256 "5e7c829832f0c3d75d7def3997717a3cf02a3c2ffef6bd39bd03f2d882edf638"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode-v#{version}-linux-amd64.tar.gz"
      sha256 "6963b35d4a688303f20b31544ef61e94a595f130a69a2955b9d273053d02575a"
    end
  end

  def install
    bin.install Dir["slmcode-v#{version}-*"].first => "slmcode"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/slmcode version")
  end
end
