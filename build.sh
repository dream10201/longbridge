#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

SDK_REPO=https://github.com/longbridge/openapi-protocol.git
SDK_DIR=third_party/openapi-protocol
PATCH_DIR=third_party/patches

build() {
	rm -f longbridge
	go get -u ./...
	go mod tidy
	go build -ldflags="-s -w" -o longbridge
}

sdk_latest_tag() {
	git ls-remote --tags "$SDK_REPO" 'go/v*' |
		sed 's@.*refs/tags/@@' | sort -t/ -k2 -V | tail -n1
}

update_sdk() {
	local tag="${1:-$(sdk_latest_tag)}"
	echo ">> 更新 $SDK_DIR 到 $tag"
	local tmp
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' RETURN
	git clone -q --depth 1 --branch "$tag" "$SDK_REPO" "$tmp"
	rm -rf "$SDK_DIR"
	cp -r "$tmp/go" "$SDK_DIR"
	git add "$SDK_DIR"
	cat <<-EOF
	>> 已替换为 $tag 原始副本并暂存。后续步骤:
	   1. git commit -m "vendor: 更新 openapi-protocol 到 $tag 原始副本"
	   2. ./build.sh patch-sdk
	   3. 确认 build 通过后提交补丁变更
	EOF
}

patch_sdk() {
	local p
	for p in "$PATCH_DIR"/*.patch; do
		echo ">> 应用 $p"
		git apply --3way "$p"
	done
	go mod tidy
	build
}

case "${1:-build}" in
build) build ;;
update-sdk) update_sdk "${2:-}" ;;
patch-sdk) patch_sdk ;;
*)
	echo "用法: $0 [build | update-sdk [go/vX.Y.Z] | patch-sdk]" >&2
	exit 1
	;;
esac
