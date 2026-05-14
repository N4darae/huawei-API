#!/usr/bin/env bash
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

read_pin() {
    local key="$1"
    local value
    value="$(grep -E "^${key}=" "${here}/VERSION" | head -n 1 | cut -d= -f2-)"
    if [ -z "${value}" ]; then
        echo "VERSION has no ${key}" >&2
        exit 1
    fi
    printf '%s' "${value}"
}

REPO="$(read_pin repo)"
COMMIT="$(read_pin commit)"
MAKEFILE="$(read_pin makefile)"
ARTIFACT="$(read_pin artifact)"
DEFAULT_INSTALL="$(read_pin install)"

SRC_DIR="${SRC_DIR:-${here}/.src}"
INSTALL_PATH="${INSTALL_PATH:-${DEFAULT_INSTALL}}"
JOBS="${JOBS:-$(nproc 2>/dev/null || echo 2)}"

if [ ! -d "${SRC_DIR}/.git" ]; then
    rm -rf "${SRC_DIR}"
    git clone --quiet "${REPO}" "${SRC_DIR}"
fi

if ! git -C "${SRC_DIR}" cat-file -e "${COMMIT}^{commit}" 2>/dev/null; then
    git -C "${SRC_DIR}" fetch --quiet origin "${COMMIT}" 2>/dev/null ||
        git -C "${SRC_DIR}" fetch --quiet origin
fi

git -C "${SRC_DIR}" checkout --quiet --detach "${COMMIT}"

got="$(git -C "${SRC_DIR}" rev-parse HEAD)"
if [ "${got}" != "${COMMIT}" ]; then
    echo "checked out ${got}, VERSION pins ${COMMIT}" >&2
    exit 1
fi

if [ ! -f "${SRC_DIR}/src/resolve.c" ]; then
    echo "src/resolve.c is absent; the pin is older than 0.9.6" >&2
    exit 1
fi

make -C "${SRC_DIR}" -f "${MAKEFILE}" -j"${JOBS}" \
    WOLFSSL_CHECK=false OPENSSL_CHECK=false PCRE_CHECK=false PAM_CHECK=false

built="${SRC_DIR}/${ARTIFACT}"
if [ ! -x "${built}" ]; then
    echo "build produced no ${ARTIFACT}" >&2
    exit 1
fi

probe="$(mktemp)"
trap 'rm -f "${probe}"' EXIT
printf 'nscache 65536\ntimeouts 1 5 30 60 180 1800 15 60 10 5\nnoforce\nauth strong\nusers "probe:CL:Aa1Bb2Cc3Dd4Ee5F"\ninternal 127.0.0.1\nflush\nallow probe\ndeny *\nsocks -p20999 -a -4\n' >"${probe}"

rc=0
timeout 5 "${built}" "${probe}" >/dev/null 2>&1 || rc=$?
if [ "${rc}" != 124 ]; then
    echo "the freshly built binary did not stay up on a minimal config (exit ${rc})" >&2
    echo "3proxy exits 0 without opening a listener when it rejects a directive" >&2
    exit 1
fi

install -D -m 0755 "${built}" "${INSTALL_PATH}"
sha="$(sha256sum "${INSTALL_PATH}" | cut -d' ' -f1)"

echo "3proxy ${COMMIT} installed at ${INSTALL_PATH}"
echo "sha256 ${sha}"
