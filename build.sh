#!/usr/bin/env bash
#
# build.sh — Cross-platform build script for Ciel Agent Go
# Produces binaries for linux, darwin, and windows on amd64 + arm64.
#
set -euo pipefail

APP_NAME="ciel"
VERSION="${VERSION:-dev}"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}"
DIST="dist"

# Target triples: GOOS/GOARCH/EXT
TARGETS=(
  "linux/amd64/"
  "linux/arm64/"
  "darwin/amd64/"
  "darwin/arm64/"
  "windows/amd64/.exe"
  "windows/arm64/.exe"
)

echo "🔨 Building ${APP_NAME} v${VERSION}"
echo "   LDFLAGS: ${LDFLAGS}"
echo ""

# Clean previous build
rm -rf "${DIST}"
mkdir -p "${DIST}"

SUCCESS=0
FAIL=0

for target in "${TARGETS[@]}"; do
  IFS='/' read -r os arch ext <<< "${target}"
  output="${DIST}/${APP_NAME}-${os}-${arch}${ext}"

  printf "  %-40s" "${os}/${arch}..."
  if GOOS="${os}" GOARCH="${arch}" CGO_ENABLED=0 go build -trimpath -ldflags "${LDFLAGS}" -o "${output}" ./cmd/hermes; then
    SIZE=$(stat --printf="%s" "${output}" 2>/dev/null || stat -f%z "${output}" 2>/dev/null || echo "?")
    echo " ✓ (${SIZE} bytes)"
    SUCCESS=$((SUCCESS + 1))
  else
    echo " ✗ FAILED"
    FAIL=$((FAIL + 1))
  fi
done

echo ""
echo "✅ Build complete: ${SUCCESS} succeeded, ${FAIL} failed"
echo "   Output: ${DIST}/"
ls -lh "${DIST}/" 2>/dev/null || dir "${DIST}\\"

if [ "${FAIL}" -gt 0 ]; then
  exit 1
fi
