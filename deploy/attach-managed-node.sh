#!/bin/sh
# Add management to an already installed H2 node without replacing its TLS keys.
set -eu
test "$(id -u)" = 0 || { echo 'Run as root' >&2; exit 1; }
agent=${1:?Usage: attach-managed-node.sh PATH_TO_PRIVATE_AGENT_JSON}
test -f /etc/tunnelx/server.json
test -x /opt/tunnelx/bin/tunnelx-server
test -f "$agent"
install -d -m 700 -o tunnelx -g tunnelx /var/lib/tunnelx
/opt/tunnelx/bin/tunnelx-server -config /etc/tunnelx/server.json -agent "$agent" -check
if test "$(realpath "$agent")" != /etc/tunnelx/agent.json; then
 install -m 640 -o root -g tunnelx "$agent" /etc/tunnelx/agent.json
fi
install -d -m 755 /etc/systemd/system/tunnelx.service.d
install -m 644 deploy/tunnelx-managed.conf /etc/systemd/system/tunnelx.service.d/managed.conf
systemctl daemon-reload
systemctl restart tunnelx.service
systemctl --no-pager --full status tunnelx.service
