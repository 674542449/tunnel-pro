#!/bin/sh
# Run inside the deployment bundle on Ubuntu / Debian with systemd and public certificates.
set -eu
test "$(id -u)" = 0 || { echo 'Run as root' >&2; exit 1; }
. /etc/os-release
case "$ID" in ubuntu|debian) ;; *) echo 'Supported: Ubuntu or Debian' >&2; exit 1;; esac
case "$(uname -m)" in aarch64|arm64) arch=arm64;; x86_64) arch=amd64;; *) echo 'Unsupported CPU' >&2; exit 1;; esac
domain=${1:?Usage: install.sh PUBLIC_DOMAIN}
case "$domain" in *[!A-Za-z0-9.-]*|'') echo 'Invalid domain' >&2; exit 1;; esac
command -v python3 >/dev/null
command -v openssl >/dev/null
command -v systemctl >/dev/null
# BBR + fq is best effort: containers and old kernels keep their defaults without failing the install.
enable_bbr() {
    conf=/etc/sysctl.d/99-tunnelx-bbr.conf
    modprobe tcp_bbr >/dev/null 2>&1 || true
    if ! grep -qw bbr /proc/sys/net/ipv4/tcp_available_congestion_control 2>/dev/null; then
        echo 'BBR is not available on this kernel; keeping the default congestion control.'
        return 0
    fi
    { printf 'net.core.default_qdisc = fq\nnet.ipv4.tcp_congestion_control = bbr\n' >"$conf" && chmod 644 "$conf"; } 2>/dev/null || true
    if modinfo tcp_bbr >/dev/null 2>&1 && test -d /etc/modules-load.d; then
        { echo tcp_bbr >/etc/modules-load.d/tunnelx-bbr.conf && chmod 644 /etc/modules-load.d/tunnelx-bbr.conf; } 2>/dev/null || true
    fi
    sysctl -q -w net.core.default_qdisc=fq >/dev/null 2>&1 || true
    sysctl -q -w net.ipv4.tcp_congestion_control=bbr >/dev/null 2>&1 || true
    if test "$(cat /proc/sys/net/ipv4/tcp_congestion_control 2>/dev/null)" = bbr; then
        echo 'BBR + fq enabled (persisted across reboots).'
    else
        echo 'This environment does not allow changing congestion control; keeping defaults.'
    fi
}
enable_bbr
certificate_source=caddy
if test -f config/cert.pem && test -f config/key.pem; then certificate_source=files; fi
certificate_source=${2:-$certificate_source}
case "$certificate_source" in caddy|files) ;; *) echo 'Certificate source must be caddy or files' >&2; exit 1;; esac
id tunnelx >/dev/null 2>&1 || useradd --system --home /nonexistent --shell /usr/sbin/nologin tunnelx
install -d -m 750 -o root -g tunnelx /etc/tunnelx
install -d -m 755 /opt/tunnelx/bin
install -m 755 "linux-$arch/tunnelx-server" /opt/tunnelx/bin/tunnelx-server.installing
mv /opt/tunnelx/bin/tunnelx-server.installing /opt/tunnelx/bin/tunnelx-server
install -m 755 "linux-$arch/tunnelx-fixture" /opt/tunnelx/bin/tunnelx-fixture
for f in server.json ech-key.json inner-cert.pem inner-key.pem; do install -m 640 -o root -g tunnelx "config/$f" "/etc/tunnelx/$f"; done
python3 - <<'PY'
import json
p='/etc/tunnelx/server.json'
with open(p) as f: c=json.load(f)
c['cert_file']='public-cert/current/cert.pem'; c['key_file']='public-cert/current/key.pem'
with open(p,'w') as f: json.dump(c,f,indent=2); f.write('\n')
PY
printf '%s\n' "$domain" >/etc/tunnelx/public-domain
install -m 755 deploy/sync-certificate.sh /opt/tunnelx/bin/sync-certificate.sh
if test "$certificate_source" = caddy; then
    /opt/tunnelx/bin/sync-certificate.sh
else
    generation="/etc/tunnelx/public-cert/files-$(date +%s)-$$"
    install -d -m 750 -o root -g tunnelx /etc/tunnelx/public-cert "$generation"
    install -m 640 -o root -g tunnelx config/cert.pem "$generation/cert.pem"
    install -m 640 -o root -g tunnelx config/key.pem "$generation/key.pem"
    openssl x509 -in "$generation/cert.pem" -noout -checkhost "$domain" -checkend 3600 >/dev/null
    openssl verify -purpose sslserver -verify_hostname "$domain" -untrusted "$generation/cert.pem" "$generation/cert.pem" >/dev/null
    cert_public=$(openssl x509 -in "$generation/cert.pem" -pubkey -noout | openssl pkey -pubin -outform DER | openssl dgst -sha256)
    key_public=$(openssl pkey -in "$generation/key.pem" -pubout -outform DER | openssl dgst -sha256)
    test "$cert_public" = "$key_public" || { echo 'Certificate and private key mismatch' >&2; exit 1; }
    ln -s "$generation" "/etc/tunnelx/public-cert/.current-$$"
    mv -Tf "/etc/tunnelx/public-cert/.current-$$" /etc/tunnelx/public-cert/current
fi
/opt/tunnelx/bin/tunnelx-server -config /etc/tunnelx/server.json -check
install -m 644 deploy/tunnelx.service deploy/tunnelx-certificate.service deploy/tunnelx-certificate.timer /etc/systemd/system/
systemctl daemon-reload
if test "$certificate_source" = caddy; then
    systemctl enable --now tunnelx-certificate.timer
else
    systemctl disable --now tunnelx-certificate.timer
fi
systemctl enable tunnelx.service
systemctl restart tunnelx.service
systemctl --no-pager --full status tunnelx.service
