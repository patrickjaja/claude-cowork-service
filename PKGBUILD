# Maintainer: Patrick Jaja <patrick.jaja@valantic.com>
# Contributor: Nayrosk

pkgname=claude-cowork-service
pkgver=1.0.0
pkgrel=1
pkgdesc="Native Linux backend for Claude Desktop Cowork"
arch=('x86_64' 'aarch64')
url="https://github.com/patrickjaja/claude-cowork-service"
license=('MIT')

depends=('systemd' 'util-linux' 'bubblewrap' 'socat' 'ripgrep')
optdepends=('claude-desktop-bin: Unofficial Linux frontend for Claude Desktop Cowork'
            'claude-code: An agentic coding tool that lives in your terminal (you can also install via native installer)')
makedepends=('go' 'bun' 'zig' 'autoconf' 'automake' 'libtool' 'gperf')

install="${pkgname}.install"

source=("${pkgname}-${pkgver}.tar.gz::${url}/archive/v${pkgver}.tar.gz")
sha256sums=('15b104b1c8db86dfa9821c05991a1173a41769c15ca3ba83c5d2895fb3e6040b')

build() {
    cd "${srcdir}/${pkgname}-${pkgver}"
    make VERSION="${pkgver}"
    make build-srt
}

package() {
    cd "${srcdir}/${pkgname}-${pkgver}"

    install -Dm755 cowork-svc-linux \
        "${pkgdir}/usr/bin/cowork-svc-linux"

    case "${CARCH}" in
        x86_64)  _srt_arch="amd64" ;;
        aarch64) _srt_arch="arm64" ;;
        *) echo "Unsupported architecture for srt: ${CARCH}" >&2; return 1 ;;
    esac
    install -Dm755 "srt/srt-linux-${_srt_arch}" \
        "${pkgdir}/usr/bin/srt-cowork"

    install -Dm644 claude-cowork.service \
        "${pkgdir}/usr/lib/systemd/user/claude-cowork.service"

    install -Dm644 LICENSE \
        "${pkgdir}/usr/share/licenses/${pkgname}/LICENSE"
}
