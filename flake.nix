{
  description = "ccpeak - Explore your Claude Code history";

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
          default = self'.packages.ccpeak;
          ccpeak = pkgs.buildGoModule {
            pname = "ccpeak";
            version = "0.3.1";

            src = lib.cleanSource ./.;

            vendorHash = "sha256-/jfshB1eK0bU4e4q5ax1L1HZ6Mf+gC9S0yqMJpL5GZU=";

            ldflags = [
              "-s"
              "-w"
              "-X github.com/ahmedelgabri/ccpeak/internal/cmd.Version=${self'.packages.ccpeak.version}"
            ];

            nativeBuildInputs = with pkgs; [
              installShellFiles
              makeWrapper
              tailwindcss_4
            ];

            subPackages = ["cmd/ccpeak"];

            preBuild = ''
              tailwindcss --input internal/web/src/app.css --output internal/web/static/style.css --minify
            '';

            postInstall = ''
              # Generate shell completions before wrapping
              $out/bin/ccpeak completion bash > ccpeak.bash
              $out/bin/ccpeak completion zsh > _ccpeak
              $out/bin/ccpeak completion fish > ccpeak.fish
              installShellCompletion --bash ccpeak.bash
              installShellCompletion --zsh _ccpeak
              installShellCompletion --fish ccpeak.fish

              # Generate and install man pages
              mkdir -p $TMPDIR/man
              $out/bin/ccpeak man $TMPDIR/man
              installManPage $TMPDIR/man/*.1

              wrapProgram $out/bin/ccpeak \
                --prefix PATH : ${pkgs.lib.makeBinPath [pkgs.git]}
            '';

            meta = {
              mainProgram = "ccpeak";
              homepage = "https://github.com/ahmedelgabri/ccpeak";
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
