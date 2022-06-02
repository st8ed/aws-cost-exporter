{
  outputs = { self, nixpkgs }:
    let
      version = "0.1.0";
      src = with lib; sources.cleanSourceWith {
        filter = name: type:
          let baseName = baseNameOf (toString name); in
            !(
              (baseName == ".github") ||
              (hasSuffix ".nix" baseName) ||
              (hasSuffix ".md" baseName)
            );
        src = lib.cleanSource ./.;
      };
      vendorSha256 = "sha256-LZomE9j6m7TAPyY/sZWVupyh8mkt8MjwUnmbYzZoUP8=";

      package = { go_1_17, buildGo117Module }: buildGo117Module {
        pname = "aws-cost-exporter";
        inherit version vendorSha256;

        src = builtins.path {
          path = src;
          name = "aws-cost-exporter-src";
        };

        ldflags =
          let
            t = "github.com/prometheus/common";
          in
          [
            "-s"
            "-X ${t}.Revision=unknown"
            "-X ${t}.Version=${version}"
            "-X ${t}.Branch=unknown"
            "-X ${t}.BuildUser=nix@nixpkgs"
            "-X ${t}.BuildDate=unknown"
            "-X ${t}.GoVersion=${lib.getVersion go_1_17}"
          ];

        preInstall = ''
          mkdir -p $out/share/aws-cost-exporter/queries
          cp $src/configs/queries/* $out/share/aws-cost-exporter/queries/
        '';

        meta = with lib; {
          homepage = "https://github.com/st8ed/aws-cost-exporter";
          license = licenses.asl20;
          platforms = platforms.unix;
        };
      };

      dockerPackage = { aws-cost-exporter, dockerTools, cacert, runCommandNoCC }: dockerTools.buildLayeredImage {
        name = "st8ed/aws-cost-exporter";
        tag = "${version}";

        contents = [
          aws-cost-exporter
        ];

        fakeRootCommands = ''
          install -dm750 -o 1000 -g 1000  \
            ./etc/aws-cost-exporter       \
            ./var/lib/aws-cost-exporter

          cp -r \
            ${aws-cost-exporter}/share/aws-cost-exporter/* \
            ./etc/aws-cost-exporter
        '';

        config = {
          Entrypoint = [ "/bin/aws-cost-exporter" ];
          Cmd = [ ];
          User = "1000:1000";
          WorkingDir = "/var/lib/aws-cost-exporter";

          Env = [
            "SSL_CERT_FILE=${cacert}/etc/ssl/certs/ca-bundle.crt"
          ];

          ExposedPorts = {
            "9100/tcp" = { };
          };

          Volumes = {
            "/var/lib/aws-cost-exporter" = { };
          };
        };
      };

      inherit (nixpkgs) lib;
      supportedSystems = [ "x86_64-linux" "aarch64-linux" ];

      forAllSystems = lib.genAttrs supportedSystems;
      nixpkgsFor = lib.genAttrs supportedSystems (system: import nixpkgs {
        inherit system;
        overlays = [ self.overlay ];
      });

    in
    {
      overlay = pkgs: _: {
        aws-cost-exporter = pkgs.callPackage package { };
        aws-cost-exporter-dockerImage = pkgs.callPackage dockerPackage { };
      };

      defaultPackage = forAllSystems (system: nixpkgsFor."${system}".aws-cost-exporter);
      packages = forAllSystems (system: {
        package = nixpkgsFor."${system}".aws-cost-exporter;
        dockerImage = nixpkgsFor."${system}".aws-cost-exporter-dockerImage;
      });
    };
}
