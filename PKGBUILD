# Maintainer: Patrick Jaja <patrick.jaja@valantic.com>
# Contributor: Nayrosk

pkgname=claude-cowork-service
pkgver=1.0.0
pkgrel=3
pkgdesc="Native Linux backend for Claude Desktop Cowork"
arch=('x86_64')
url="https://github.com/patrickjaja/claude-cowork-service"
license=('MIT')

depends=('systemd' 'util-linux')
makedepends=('go')

install="${pkgname}.install"

source=("${pkgname}-${pkgver}.tar.gz::${url}/archive/v${pkgver}.tar.gz")
sha256sums=('15b104b1c8db86dfa9821c05991a1173a41769c15ca3ba83c5d2895fb3e6040b')

build() {
    cd "${srcdir}/${pkgname}-${pkgver}"
    make VERSION="${pkgver}"
}

package() {
    cd "${srcdir}/${pkgname}-${pkgver}"

    install -Dm755 cowork-svc-linux \
        "${pkgdir}/usr/bin/cowork-svc-linux"

    install -Dm644 dist/claude-cowork.service \
        "${pkgdir}/usr/lib/systemd/user/claude-cowork.service"

    install -Dm644 LICENSE \
        "${pkgdir}/usr/share/licenses/${pkgname}/LICENSE"
}
