# Exercise the generated install method with a small stand-in for the Homebrew DSL.
require "pathname"
require "fileutils"

class InstallPath < Pathname
  def install(source)
    mkpath
    FileUtils.cp(source, self / File.basename(source))
  end
end

class Formula
  class << self
    def desc(*)
    end
    alias homepage desc
    alias license desc
    alias depends_on desc
    alias url desc
    alias sha256 desc
    alias test desc
    alias service desc

    def on_macos
      yield
    end
    alias on_arm on_macos
    alias on_intel on_macos
  end

  def bin
    InstallPath.new(prefix / "bin")
  end

  def var
    Pathname.new(ENV.fetch("TEST_VAR"))
  end

  def prefix
    Pathname.new(ENV.fetch("TEST_PREFIX"))
  end
end

load ARGV.fetch(0)
Refract.new.install
