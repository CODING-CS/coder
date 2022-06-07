#!/usr/bin/env bash

# This script builds a single Go binary of Coder with the given parameters.
#
# Usage: ./build_go.sh [--version v1.2.3+devel.abcdef] [--os linux] [--arch amd64] [--output path/to/output] [--slim]
#
# Defaults to linux:amd64 with slim disabled, but can be controlled with GOOS,
# GOARCH and CODER_SLIM_BUILD=1. If no version is specified, defaults to the
# version from ./version.sh.
#
# Unless overridden via --output, the built binary will be dropped in
# "$repo_root/dist/coder(-slim)?_$version_$os_$arch" (with a ".exe" suffix for
# windows builds) and the absolute path to the binary will be printed to stdout
# on completion.

set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

version=""
os="${GOOS:-linux}"
arch="${GOARCH:-amd64}"
slim="${CODER_SLIM_BUILD:-0}"
output_path=""

args="$(getopt -o "" -l version:,os:,arch:,output:,slim -- "$@")"
eval set -- "$args"
while true; do
    case "$1" in
    --version)
        version="$2"
        shift 2
        ;;
    --os)
        os="$2"
        shift 2
        ;;
    --arch)
        arch="$2"
        shift 2
        ;;
    --output)
        output_path="$(realpath "$2")"
        shift 2
        ;;
    --slim)
        slim=1
        shift
        ;;
    --)
        shift
        break
        ;;
    *)
        error "Unrecognized option: $1"
        ;;
    esac
done

if [[ "$version" == "" ]]; then
    cdself
    version="$(./version.sh)"
fi

build_args=(
    -ldflags "-s -w -X 'github.com/coder/coder/buildinfo.tag=$version'"
)
if [[ "$slim" == 0 ]]; then
    build_args+=(-tags embed)
fi

# cd to the root of the repo.
cdroot

# Compute default output path.
if [[ "$output_path" == "" ]]; then
    dist_dir="dist"
    mkdir -p "$dist_dir"
    output_path="${dist_dir}/coder_${version}_${os}_${arch}"
    if [[ "$os" == "windows" ]]; then
        output_path+=".exe"
    fi
    output_path="$(realpath "$output_path")"
fi
build_args+=(-o "$output_path")

CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
    "${build_args[@]}" \
    ./cmd/coder 1>&2

echo -n "$output_path"