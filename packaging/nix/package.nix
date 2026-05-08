{ lib
, stdenv
, buildGoModule
, fetchFromGitHub
, fetchurl
, makeWrapper
, bubblewrap
, socat
, ripgrep
, srtBinaries ? null
}:

let
  packageVersion = "1.0.53";
  srtSystem =
    if stdenv.hostPlatform.isAarch64 then "aarch64-linux"
    else if stdenv.hostPlatform.isx86_64 then "x86_64-linux"
    else throw "claude-cowork-service: unsupported SRT platform ${stdenv.hostPlatform.system}";
  srtBinary =
    if stdenv.hostPlatform.isAarch64 then "srt-linux-arm64"
    else if stdenv.hostPlatform.isx86_64 then "srt-linux-amd64"
    else throw "claude-cowork-service: unsupported SRT platform ${stdenv.hostPlatform.system}";
  # Replaced by the release workflow after the srt-linux-* assets are uploaded.
  # Pre-release CI passes generated binaries via srtBinaries instead.
  srtHashes = {
    x86_64-linux = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
    aarch64-linux = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
  };
  fetchedSrtBinary = fetchurl {
    url = "https://github.com/patrickjaja/claude-cowork-service/releases/download/v${packageVersion}/${srtBinary}";
    hash = srtHashes.${srtSystem};
  };
  srtBinaryPath =
    if srtBinaries != null
    then "${srtBinaries}/${srtBinary}"
    else fetchedSrtBinary;
  runtimePath = lib.makeBinPath [ bubblewrap socat ripgrep ];
in
buildGoModule rec {
  pname = "claude-cowork-service";
  version = packageVersion;

  src = fetchFromGitHub {
    owner = "patrickjaja";
    repo = "claude-cowork-service";
    rev = "v${version}";
    hash = "sha256-Z75W8mXTQ8u3TZd6a+Pw9pMQ+UshKozEE8Bir5AA8Gg=";
  };

  vendorHash = "sha256-g+yaVIx4jxpAQ/+WrGKxhVeliYx7nLQe/zsGpxV4Fn4=";

  env.CGO_ENABLED = 0;

  # srt-cowork is a bun-compiled executable with its JS payload appended at the
  # end of the file; the default strip pass would clip that payload and degrade
  # the binary into vanilla bun. Skip stripping for the whole derivation —
  # cowork-svc-linux is a Go PIE binary that we already build with -trimpath.
  dontStrip = true;

  nativeBuildInputs = [ makeWrapper ];

  ldflags = [
    "-X main.version=${version}"
  ];

  buildFlags = [
    "-trimpath"
  ];

  postInstall = ''
    mv $out/bin/${pname} $out/bin/cowork-svc-linux
    install -Dm755 ${srtBinaryPath} $out/bin/srt-cowork
    wrapProgram $out/bin/cowork-svc-linux \
      --prefix PATH : "$out/bin:${runtimePath}"

    mkdir -p $out/lib/systemd/user
    cp $src/claude-cowork.service $out/lib/systemd/user/claude-cowork.service
  '';

  meta = with lib; {
    description = "Native Linux backend for Claude Desktop's Cowork feature";
    homepage = "https://github.com/patrickjaja/claude-cowork-service";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "aarch64-linux" ];
    maintainers = [ ];
    mainProgram = "cowork-svc-linux";
  };
}
