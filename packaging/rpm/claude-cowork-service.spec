%define _build_id_links none
%global debug_package %{nil}
# srt-cowork is a bun-compiled executable with its JS payload appended at the
# end of the file; rpmbuild's default brp-strip / brp-strip-static-archive
# would clip that payload and degrade the binary into vanilla bun.
# Skip all post-install binary processing.
%global __os_install_post %{nil}

Name:           claude-cowork-service
Version:        %{pkg_version}
Release:        1%{?dist}
Summary:        Native Linux backend for Claude Desktop's Cowork feature

License:        MIT
URL:            https://github.com/patrickjaja/claude-cowork-service

ExclusiveArch:  x86_64 aarch64

Requires:       systemd
Requires:       bubblewrap
Requires:       socat
Requires:       ripgrep

%description
Reverse-engineered from Windows cowork-svc.exe. Implements the
length-prefixed JSON-over-Unix-socket protocol that Claude Desktop
expects, running commands directly on the host instead of in a VM.

%install
rm -rf %{buildroot}

# Install binary
mkdir -p %{buildroot}/usr/bin
install -m755 %{_sourcedir}/cowork-svc-linux %{buildroot}/usr/bin/cowork-svc-linux
install -m755 %{_sourcedir}/srt-cowork %{buildroot}/usr/bin/srt-cowork

# Install systemd user service
mkdir -p %{buildroot}/usr/lib/systemd/user
install -m644 %{_sourcedir}/claude-cowork.service %{buildroot}/usr/lib/systemd/user/claude-cowork.service

%post
echo ""
echo "claude-cowork-service installed successfully!"
echo ""
echo "Enable and start the service with:"
echo "  systemctl --user daemon-reload"
echo "  systemctl --user enable --now claude-cowork"
echo ""

%files
/usr/bin/cowork-svc-linux
/usr/bin/srt-cowork
/usr/lib/systemd/user/claude-cowork.service
