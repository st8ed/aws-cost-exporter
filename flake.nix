{
  outputs = { self, nixpkgs }:
    let
      version = "0.3.6";
      chartVersion = "0.1.6";
      vendorSha256 = "sha256-e3AUY+qKnLEugLviQxTK1Dj6mIuo2oCu8pmjuLqrbio=";
      dockerPackageTag = "st8ed/aws-cost-exporter:${version}";

      src = with lib; builtins.path {
        name = "aws-cost-exporter-src";
        path = sources.cleanSourceWith rec {
          filter = name: type:
            let baseName = baseNameOf (toString name); in
              !(
                (baseName == ".github") ||
                (hasSuffix ".nix" baseName) ||
                (hasSuffix ".md" baseName) ||
                (hasPrefix "${src}/deployments" name)
              );
          src = lib.cleanSource ./.;
        };
      };

      src-chart = with lib; builtins.path {
        name = "aws-cost-exporter-chart-src";
        path = lib.cleanSource ./deployments/chart;
      };

      package = { go_1_17, buildGo117Module }: buildGo117Module {
        pname = "aws-cost-exporter";
        inherit version vendorSha256 src;

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

      dockerPackage = { pkgs, aws-cost-exporter, dockerTools, cacert, skopeo, moreutils, runCommandNoCC }:
        let
          # We compress image layers so the digest
          # will be reproducible when pushing to registry
          buildCompressedImage = stream: runCommandNoCC "aws-cost-exporter-dockerImage"
            {
              buildInputs = [ skopeo moreutils ];
            } ''
            # Piping archive stream to skopeo isn't working correctly
            ${stream} > archive.tar

            skopeo --insecure-policy copy docker-archive:./archive.tar dir:$out \
              --format v2s2 \
              --dest-compress
          '';

        in
        buildCompressedImage (dockerTools.streamLayeredImage {
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
        });

      helmChart = { pkgs, aws-cost-exporter-dockerImage, kubernetes-helm, jq, gnused }: pkgs.runCommand "aws-cost-exporter-chart-${chartVersion}.tgz"
        {
          src = src-chart;
          buildInputs = [ kubernetes-helm jq gnused ];
        } ''
        cp -r $src ./chart
        chmod -R a+w ./chart

        sed -i \
          -e 's/^version: 0\.0\.0$/version: ${chartVersion}/' \
          -e 's/^appVersion: "0\.0\.0"$/appVersion: "${version}"/' \
          ./chart/Chart.yaml

        digest="sha256:$(sha256sum "${aws-cost-exporter-dockerImage}/manifest.json" | cut -d' ' -f1)"
        echo "Digest: $digest"

        sed -i \
          -e 's|^image:.*$|image: "${dockerPackageTag}@'$digest'"|' \
          ./chart/values.yaml

        mkdir -p ./package
        helm package --destination ./package ./chart

        mv ./package/*.tgz $out
      '';

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
        aws-cost-exporter-helmChart = pkgs.callPackage helmChart { };
      };

      defaultPackage = forAllSystems (system: nixpkgsFor."${system}".aws-cost-exporter);
      packages = forAllSystems (system: {
        package = nixpkgsFor."${system}".aws-cost-exporter;
        dockerImage = nixpkgsFor."${system}".aws-cost-exporter-dockerImage;
        helmChart = nixpkgsFor."${system}".aws-cost-exporter-helmChart;

        inherit src src-chart;
      });
    };
}
