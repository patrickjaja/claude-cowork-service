{ lib
, stdenv
, buildGoModule
, fetchFromGitHub
, makeWrapper
, bubblewrap
, socat
, ripgrep
}:

let
  srtBinary =
    if stdenv.hostPlatform.isAarch64 then "srt-linux-arm64"
    else if stdenv.hostPlatform.isx86_64 then "srt-linux-amd64"
    else throw "claude-cowork-service: unsupported SRT platform ${stdenv.hostPlatform.system}";
  runtimePath = lib.makeBinPath [ bubblewrap socat ripgrep ];
in
buildGoModule rec {
  pname = "claude-cowork-service";
  version = "1.0.53";

  src = fetchFromGitHub {
    owner = "patrickjaja";
    repo = "claude-cowork-service";
    rev = "v${version}";
    hash = "sha256-VGFM7OxFD1nYn+FhDonI7J99f8xC6CLbcZ8sJU1dQ+4=";
  };

  vendorHash = null;

  env.CGO_ENABLED = 0;

  nativeBuildInputs = [ makeWrapper ];

  ldflags = [
    "-X main.version=${version}"
  ];

  buildFlags = [
    "-trimpath"
  ];

  postInstall = ''
    mv $out/bin/${pname} $out/bin/cowork-svc-linux
    install -Dm755 $src/srt/${srtBinary} $out/bin/srt-cowork
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
