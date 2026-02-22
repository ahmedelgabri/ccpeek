{
  description = "ccexplore - Explore your Claude Code history";

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
          default = self'.packages.ccexplore;
          ccexplore = pkgs.buildGoModule {
            pname = "ccexplore";
            version = "0.1.0";

            src = lib.cleanSource ./.;

            vendorHash = "sha256-yAVWC5AIVVjDBSDHINyGxoUJdURVdv8IJdfRMom96CU=";

            ldflags = [
              "-s"
              "-w"
              "-X github.com/ahmedelgabri/ccexplore/internal/cmd.Version=${self'.packages.ccexplore.version}"
            ];

            nativeBuildInputs = with pkgs; [
              installShellFiles
              makeWrapper
              tailwindcss_4
            ];

            subPackages = ["cmd/ccexplore"];

            preBuild = ''
              tailwindcss --input internal/web/src/app.css --output internal/web/static/style.css --minify
            '';

            postInstall = ''
              # Generate shell completions before wrapping
              $out/bin/ccexplore completion bash > ccexplore.bash
              $out/bin/ccexplore completion zsh > _ccexplore
              $out/bin/ccexplore completion fish > ccexplore.fish
              installShellCompletion --bash ccexplore.bash
              installShellCompletion --zsh _ccexplore
              installShellCompletion --fish ccexplore.fish

              # Generate and install man pages
              mkdir -p $TMPDIR/man
              $out/bin/ccexplore man $TMPDIR/man
              installManPage $TMPDIR/man/*.1

              wrapProgram $out/bin/ccexplore \
                --prefix PATH : ${pkgs.lib.makeBinPath [pkgs.git]}
            '';

            meta = {
              mainProgram = "ccexplore";
              homepage = "https://github.com/ahmedelgabri/ccexplore";
              description = "Explore your Claude Code history";
              license = lib.licenses.mit;
              platforms = lib.platforms.unix;
            };
          };
        };

        treefmt = {
          projectRootFile = "flake.nix";

          programs = {
            gofumpt.enable = true;
            prettier = {
              enable = true;
              includes = [
                "*.md"
                "*.yml"
                "*.yaml"
                "*.json"
                "*.svg"
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
