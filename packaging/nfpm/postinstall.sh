#!/bin/sh
# Runs after the package is installed on deb/rpm/Arch. It creates the dedicated system user
# and state directory the hardened unit needs, refreshes systemd, and prints the next steps.
# The unit ships disabled: the operator prepares the config, then enables the service.
set -e

# A locked-down login shell for the service account.
nologin_shell=/usr/sbin/nologin
[ -x "$nologin_shell" ] || nologin_shell=/sbin/nologin
[ -x "$nologin_shell" ] || nologin_shell=/bin/false

if ! getent group filefin >/dev/null 2>&1; then
	groupadd --system filefin 2>/dev/null || addgroup --system filefin 2>/dev/null || true
fi
if ! getent passwd filefin >/dev/null 2>&1; then
	useradd --system --gid filefin --home-dir /var/lib/filefin \
		--shell "$nologin_shell" --comment "FileFin media server" filefin 2>/dev/null ||
		adduser --system --ingroup filefin --home /var/lib/filefin \
			--shell "$nologin_shell" filefin 2>/dev/null || true
fi

mkdir -p /var/lib/filefin
chown filefin:filefin /var/lib/filefin 2>/dev/null || true
chmod 0750 /var/lib/filefin

command -v systemctl >/dev/null 2>&1 && systemctl daemon-reload >/dev/null 2>&1 || true

cat <<'EOF'

FileFin is installed. The systemd unit is present but disabled. To finish setup:

  1. sudo -u filefin HOME=/var/lib/filefin filefin setup --port 8080
       (copy the printed setup URL; use --port 80 to serve on the default HTTP port)
  2. sudo systemctl enable --now filefin
  3. open the setup URL in a browser and set the admin account + data folder

ffmpeg is recommended (for transcoding non-browser-native formats) but not required.
EOF

exit 0
