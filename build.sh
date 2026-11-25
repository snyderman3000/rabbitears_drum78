#!/bin/sh
# Builds the RG Nano package: dist/RabbitEars-v<version>-RGNano.zip, holding
# Applications/RabbitEars.opk and the Media/TV folders.
#
#   sudo apt install gcc-arm-linux-gnueabihf autoconf automake libtool pkg-config squashfs-tools zip
#   ./build.sh
set -e
cd "$(dirname "$0")"
VERSION=$(grep -o 'const version = "[^"]*"' main.go | cut -d'"' -f2)
[ -f go.work ] || [ -n "$GOWORK" ] || go mod tidy
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags="-s -w" -o package/opk/rabbitears .
[ -x tvplay/out/tvplay ] && [ tvplay/out/tvplay -nt tvplay/tvplay.c ] || tvplay/build.sh
cp tvplay/out/tvplay package/opk/tvplay
go run . -icon package/opk/rabbitears.png -icon-size 32
rm -rf dist && mkdir -p dist/stage/Applications
mksquashfs package/opk dist/stage/Applications/RabbitEars.opk -all-root -noappend -no-exports -no-xattrs -quiet
cp -r package/Media dist/stage/
(cd dist/stage && zip -qr "../RabbitEars-v$VERSION-RGNano.zip" Applications Media)
cp dist/stage/Applications/RabbitEars.opk dist/
rm -rf dist/stage
cp converter/vhsify dist/vhsify
(cd converter && zip -q ../dist/vhsify-pi-docker.zip vhsify watch.sh Dockerfile docker-compose.example.yml)
echo "Built dist/RabbitEars-v$VERSION-RGNano.zip, dist/RabbitEars.opk, dist/vhsify and dist/vhsify-pi-docker.zip"
