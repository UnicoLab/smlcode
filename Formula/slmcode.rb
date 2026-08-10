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
  version "0.13.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_darwin_arm64"
      sha256 "e45a773262f3d6d82753f1ebf2f394720955382670fb7b8f99c4f5b87070210a"
    end
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_darwin_amd64"
      sha256 "513d49f82cce4382ba775a046b86638c33af49b1057fd9896b9b19646b081048"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_linux_arm64"
      sha256 "95469b38f5457499758d2a1c903915b79c9468ae5e9d5e47ba972fc6dd3f3955"
    end
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_linux_amd64"
      sha256 "46174a3c6371d7f440622ea644d73bf58e5eb9ce0e60740ed5e0094f319e8ad8"
    end
  end

  def install
    bin.install Dir["slmcode_*"].first => "slmcode"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/slmcode version")
  end
end
