class Mesh < Formula
  desc "Autonomous AI Agent Ops, Quota Pacing & Cross-AI Context Platform"
  homepage "https://github.com/VinnyVanGogh/agent-mesh"
  version "0.1.0"
  license "Apache-2.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/VinnyVanGogh/agent-mesh/releases/download/v0.1.0/agent-mesh_0.1.0_darwin_arm64.tar.gz"
      sha256 "d95e984855edb745adba3f349c68db6bd8e86d17e52b5feb4cc32ded41093e99"
    else
      url "https://github.com/VinnyVanGogh/agent-mesh/releases/download/v0.1.0/agent-mesh_0.1.0_darwin_amd64.tar.gz"
      sha256 "9f4099779682814b9ecbc31c8f5c6b80e2d7653785e43cb858a41d7271bfaa99"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/VinnyVanGogh/agent-mesh/releases/download/v0.1.0/agent-mesh_0.1.0_linux_arm64.tar.gz"
      sha256 "8bacc689bfec2bc802882ff2e125ec0fabd19a5fe4e7aa18c32a268e37774924"
    else
      url "https://github.com/VinnyVanGogh/agent-mesh/releases/download/v0.1.0/agent-mesh_0.1.0_linux_amd64.tar.gz"
      sha256 "5ae26685b6ac865d38854ea8ead5fd058a5e2745724d38410ecff70cc620ab0a"
    end
  end

  def install
    bin.install "mesh"
    bin.install "meshd"
  end

  def caveats
    <<~EOS
      To enable shell aliases and automatic model routing, add this to your ~/.zshrc:
        eval "$(mesh init --shell)"

      To start the background telemetry and rate-limit watcher daemon:
        mesh init
    EOS
  end

  test do
    assert_match "mesh version 0.1.0", shell_output("#{bin}/mesh version")
  end
end
