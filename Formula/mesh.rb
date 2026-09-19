class Mesh < Formula
  desc "Autonomous AI Agent Ops, Quota Pacing & Cross-AI Context Platform"
  homepage "https://github.com/VinnyVanGogh/agent-mesh"
  version "0.1.0"
  license "Apache-2.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/VinnyVanGogh/agent-mesh/releases/download/v0.1.0/agent-mesh_0.1.0_darwin_arm64.tar.gz"
    else
      url "https://github.com/VinnyVanGogh/agent-mesh/releases/download/v0.1.0/agent-mesh_0.1.0_darwin_amd64.tar.gz"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/VinnyVanGogh/agent-mesh/releases/download/v0.1.0/agent-mesh_0.1.0_linux_arm64.tar.gz"
    else
      url "https://github.com/VinnyVanGogh/agent-mesh/releases/download/v0.1.0/agent-mesh_0.1.0_linux_amd64.tar.gz"
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
