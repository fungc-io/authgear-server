{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      nixpkgs,
      flake-utils,
      ...
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
        };
      in
      {
        devShells.default = pkgs.mkShell {
          packages = [
            # 1.24.1
            pkgs.go
            # Any nodejs 20 is fine.
            pkgs.nodejs_20
            # The version of python3 does not matter that much.
            pkgs.python3

            pkgs.pkg-config

            (pkgs.golangci-lint.overrideAttrs (
              prev:
              let
                version = "1.64.8";
              in
              {
                inherit version;
                src = pkgs.fetchFromGitHub {
                  owner = "golangci";
                  repo = "golangci-lint";
                  rev = "v${version}";
                  hash = "sha256-H7IdXAleyzJeDFviISitAVDNJmiwrMysYcGm6vAoWso=";
                };
                vendorHash = "sha256-i7ec4U4xXmRvHbsDiuBjbQ0xP7xRuilky3gi+dT1H10=";
                # We do not actually override anything here,
                # but if we do not repeat this, ldflags refers to the original version.
                ldflags = [
                  "-s"
                  "-X main.version=${version}"
                  "-X main.commit=v${version}"
                  "-X main.date=19700101-00:00:00"
                ];
              }
            ))
          ];
          buildInputs = [
            pkgs.icu
            pkgs.vips
            # file includes libmagic.
            pkgs.file
          ];
          nativeBuildInputs = [
            pkgs.pkg-config
            pkgs.icu
            pkgs.vips
            pkgs.file
          ];
        };
      }
    );
}
