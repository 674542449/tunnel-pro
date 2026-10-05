set -eu
cp -a /etc/caddy/Caddyfile /etc/caddy/Caddyfile.tunnelx-before
if ! grep -q '^test.xiaguamail.com' /etc/caddy/Caddyfile; then
cat >> /etc/caddy/Caddyfile <<'CADDY'

test.xiaguamail.com {
    respond "Web service is online." 200
}
CADDY
fi
caddy validate --config /etc/caddy/Caddyfile
systemctl reload caddy
