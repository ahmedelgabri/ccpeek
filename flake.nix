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
        "x86_64-darwin"
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
          ccpeek = pkgs.buildGoModule {
            pname = "ccpeek";
            version = "1.7.1";

            src = lib.cleanSource ./.;

            vendorHash = "sha256-ems9UlyOiLhseCW+MjAPPsYklKSJM/m+BUIHoC4v59g=";

            tags = ["sqlite_fts5"];

            ldflags = [
              "-s"
              "-w"
              "-X github.com/ahmedelgabri/ccpeek/internal/cmd.Version=${self'.packages.ccpeek.version}"
            ];

            nativeBuildInputs = with pkgs; [
              installShellFiles
              makeWrapper
              tailwindcss_4
            ];

            subPackages = ["cmd/ccpeek"];

            preBuild = ''
              tailwindcss --input internal/web/src/app.css --output internal/web/static/style.css --minify
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
            go-tools # staticcheck, etc...
            gofumpt
            gomodifytags
            gopls
            gotools # goimports
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
