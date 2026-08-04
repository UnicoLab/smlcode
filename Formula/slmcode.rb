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
  version "0.7.3"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_darwin_arm64"
      sha256 "c4a0a0221396b456705a24bb04fcc79c2b36284377e89242156700d0a87b1f02"
    end
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_darwin_amd64"
      sha256 "43ea3aefeb0fdb2bb06bfcfe35382bf7a67e0c090206030e31881f7f833b490f"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_linux_arm64"
      sha256 "819de6fc07d797132237dcac543b30ee9f5071fa21325b4510b8b474d4dbde08"
    end
    on_intel do
      url "https://github.com/UnicoLab/smlcode/releases/download/v#{version}/slmcode_#{version}_linux_amd64"
      sha256 "16e5aa759481e721a1a4714da7f9faff2cbcd121cad79ac43d96378b4e2e7c7d"
    end
  end

  def install
    bin.install Dir["slmcode_*"].first => "slmcode"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/slmcode version")
  end
end
