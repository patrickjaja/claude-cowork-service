{ lib
, buildGoModule
, fetchFromGitHub
}:

buildGoModule rec {
  pname = "claude-cowork-service";
  version = "1.0.0";

  src = fetchFromGitHub {
    owner = "patrickjaja";
    repo = "claude-cowork-service";
    rev = "v${version}";
    hash = "sha256-/P2NXxZn92wysy1kPp7MdQvrzgnyfRPUe/5I24dH1U8=";
  };

  vendorHash = null; # Pure stdlib, no external dependencies

  env.CGO_ENABLED = 0;

  ldflags = [
    "-X main.version=${version}"
  ];

  buildFlags = [
    "-trimpath"
  ];

  postInstall = ''
    mv $out/bin/${pname} $out/bin/cowork-svc-linux

    mkdir -p $out/lib/systemd/user
    cp ${src}/dist/claude-cowork.service $out/lib/systemd/user/claude-cowork.service
  '';

  meta = with lib; {
    description = "Native Linux backend for Claude Desktop's Cowork feature";
    homepage = "https://github.com/patrickjaja/claude-cowork-service";
    license = licenses.mit;
    platforms = [ "x86_64-linux" ];
    maintainers = [ ];
    mainProgram = "cowork-svc-linux";
  };
}
