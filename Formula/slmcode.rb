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
  version "0.7.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_darwin_arm64"
      sha256 "f77ca43a48ccb6aeee909394b0e35fd9bef6f4fc58c8a3b11527ac24defbe6e3"
    end
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_darwin_amd64"
      sha256 "821305a2ed4514e5801be3c15f94307101846bfc08db5ce14399d5939de21f76"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_linux_arm64"
      sha256 "695a6dd7e3228f2a5465cc96e95c7a91260862c8840ab103364bb9d3380e0a77"
    end
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_linux_amd64"
      sha256 "1fe1dbc0f734daa33b915d8b4ac86e5cb02446b12e32dc85fab7a24fa01c44a1"
    end
  end

  def install
    bin.install Dir["slmcode_*"].first => "slmcode"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/slmcode version")
  end
end
