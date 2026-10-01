{ pkgs ? import <nixpkgs> {} }:
pkgs.mkShell {
  packages = with pkgs; [
    go
    git
    gcc
  ];

  shellHook = ''
    echo "combox-backend nix shell: $(go version)"
  '';
}
