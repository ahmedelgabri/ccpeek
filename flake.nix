{
  description = "ccpeek - Explore your Claude Code history";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixpkgs-unstable";
    flake-parts.url = "github:hercules-ci/flake-parts";
    treefmt-nix.url = "github:numtide/treefmt-nix";
  };

  outputs = inputs:
    inputs.flake-parts.lib.mkFlake {inherit inputs;} {
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];

      imports = [
        inputs.treefmt-nix.flakeModule
      ];

      perSystem = {
        pkgs,
        config,
        self',
        lib,
        ...
      }: {
        packages = {
          default = self'.packages.ccpeek;

          # The SPA, built from ui/ and embedded into the Go binary below —
          # a binary without it serves an empty UI. pnpm dependencies are a
          # fixed-output derivation; bump its hash when ui/pnpm-lock.yaml
          # changes.
          ccpeek-ui = pkgs.stdenv.mkDerivation (finalAttrs: {
            pname = "ccpeek-ui";
            version = self'.packages.ccpeek.version;

            src = lib.cleanSource ./ui;

            nativeBuildInputs = with pkgs; [
              nodejs
              pnpm_10.configHook
            ];

            pnpmDeps = pkgs.pnpm_10.fetchDeps {
              inherit (finalAttrs) pname version src;
              fetcherVersion = 2;
              hash = "sha256-LpguLIhF2b9luh4kc5cZ+N/pg+D+mbSHYqs3hmFZKOA=";
            };

            buildPhase = ''
              runHook preBuild
              # The config's outDir points outside ui/ (../internal/webui/dist),
              # which doesn't exist in this sandbox — override it.
              pnpm exec vite build --outDir dist --emptyOutDir
              runHook postBuild
            '';

            installPhase = ''
              runHook preInstall
              cp -r dist $out
              runHook postInstall
            '';
          });

          ccpeek = pkgs.buildGoModule {
            pname = "ccpeek";
            version = "2.0.0";

            src = lib.cleanSource ./.;

            vendorHash = "sha256-4EnkrZU0jTweP5icq+LfzGN20eKLMls/yk4Qx8cwDNw=";

            ldflags = [
              "-s"
              "-w"
              "-X github.com/ahmedelgabri/ccpeek/internal/cmd.Version=${self'.packages.ccpeek.version}"
            ];

            nativeBuildInputs = with pkgs; [
              installShellFiles
              makeWrapper
            ];

            subPackages = ["cmd/ccpeek"];

            preBuild = ''
              cp -r ${self'.packages.ccpeek-ui}/. internal/webui/dist/
            '';

            postInstall = ''
              # Generate shell completions before wrapping
              $out/bin/ccpeek completion bash > ccpeek.bash
              $out/bin/ccpeek completion zsh > _ccpeek
              $out/bin/ccpeek completion fish > ccpeek.fish
              installShellCompletion --bash ccpeek.bash
              installShellCompletion --zsh _ccpeek
              installShellCompletion --fish ccpeek.fish

              # Generate and install man pages
              mkdir -p $TMPDIR/man
              $out/bin/ccpeek man $TMPDIR/man
              installManPage $TMPDIR/man/*.1

              wrapProgram $out/bin/ccpeek \
                --prefix PATH : ${pkgs.lib.makeBinPath [pkgs.git]}
            '';

            meta = {
              mainProgram = "ccpeek";
              homepage = "https://github.com/ahmedelgabri/ccpeek";
              description = "Explore your Claude Code history";
              license = lib.licenses.mit;
              platforms = lib.platforms.unix;
            };
          };
        };

        treefmt = {
          projectRootFile = "flake.nix";
          # prettier-plugin-go-template is an npm dep, not available in nix sandbox
          flakeCheck = false;

          programs = {
            gofumpt.enable = true;
            prettier = {
              enable = true;
              includes = [
                "*.md"
                "*.yml"
                "*.yaml"
                "*.json"
                "*.js"
                "*.ts"
                "*.html"
              ];
            };
            alejandra.enable = true;
          };
        };

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            go-tools # includes staticcheck
            gofumpt
            gomodifytags
            gopls
            gotools # goimports
            govulncheck
            just
            lefthook
            nixd
            nodejs
            oxlint
            pnpm
            tsx
            typescript
          ];

          inputsFrom = [config.treefmt.build.devShell];

          shellHook =
            /*
            bash
            */
            ''
              # avoid overriding global git hooks
              git config core.hooksPath .hooks
              lefthook install --force
            '';
        };
      };
    };
}
