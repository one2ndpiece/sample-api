{
  description = "Development environment for sample-api";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forEachSystem = nixpkgs.lib.genAttrs systems;
    in
    {
      packages = forEachSystem (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
          goSource =
            {
              x86_64-linux = {
                url = "https://go.dev/dl/go1.26.4.linux-amd64.tar.gz";
                hash = "sha256-EVPT1Q4Kx2S0R63+BcK88I6InUKgLg/gJZvUf2czrX8=";
              };
              aarch64-linux = {
                url = "https://go.dev/dl/go1.26.4.linux-arm64.tar.gz";
                hash = "sha256-73WK58bPkmfJwO8IC4ll9FPYmrLSXZ6yLeRAWSUjh2g=";
              };
            }
            .${system};

          go = pkgs.stdenvNoCC.mkDerivation {
            pname = "go";
            version = "1.26.4";
            src = pkgs.fetchurl goSource;
            dontBuild = true;

            installPhase = ''
              runHook preInstall
              mkdir -p "$out"
              cp -R . "$out/"
              runHook postInstall
            '';
          };
        in
        {
          inherit go;
          default = go;
        }
      );

      devShells = forEachSystem (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
        in
        {
          default = pkgs.mkShell {
            packages = [
              self.packages.${system}.go
              pkgs.gopls
              pkgs.gotools
              pkgs.govulncheck
              pkgs.just
              pkgs.kubectl
              pkgs.kustomize
              pkgs.nixfmt
              pkgs.jq
              pkgs.yq-go
            ];

            CGO_ENABLED = "0";
            GOTOOLCHAIN = "local";

            shellHook = ''
              echo "sample-api dev shell: $(go version)"
            '';
          };
        }
      );

      formatter = forEachSystem (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
        in
        pkgs.writeShellApplication {
          name = "format-nix";
          runtimeInputs = [ pkgs.nixfmt ];
          text = ''
            find . -name '*.nix' -not -path './.direnv/*' -print0 | xargs -0 -r nixfmt
          '';
        }
      );
    };
}
