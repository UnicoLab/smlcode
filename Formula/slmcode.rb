# Homebrew formula for SLMCode.
#
# One-liner:
#   brew install --formula https://raw.githubusercontent.com/UnicoLab/smlcode/main/Formula/slmcode.rb
#
# Or tap this repo:
#   brew tap UnicoLab/smlcode https://github.com/UnicoLab/smlcode
#   brew install slmcode
class Slmcode < Formula
  desc "Coding harness for SLMs and any OpenAI-compatible LLM"
  homepage "https://unicolab.ai"
  version "0.5.16"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_darwin_arm64"
      sha256 "eaed1119f6c5a90bbe31dd7efbc01ae258cccef2928d5373db333a595a3636de"
    end
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_darwin_amd64"
      sha256 "5451336c1559b8b8669b6ba41e6ab35d2cee95c73d5ff4fd250e3827a843c15f"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_linux_arm64"
      sha256 "5d796041072636e4abbbf38119dd3acdeef20d9361d86c7ef45f83416e221c95"
    end
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_linux_amd64"
      sha256 "3c41484785829f0a3eaea745968e3ae26c76b7e566b62bb99229bc19939059b0"
    end
  end

  def install
    bin.install Dir["slmcode_*"].first => "slmcode"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/slmcode version")
  end
end
