#!/bin/sh
set -eu
chmod 755 /opt/tunnelx/bin/tunnelx-server.new /opt/tunnelx/bin/tunnelx-fixture.new
mv /opt/tunnelx/bin/tunnelx-server.new /opt/tunnelx/bin/tunnelx-server
systemctl restart tunnelx.service
systemctl stop tunnelx-acceptance-fixture.service
mv /opt/tunnelx/bin/tunnelx-fixture.new /opt/tunnelx/bin/tunnelx-fixture
install -d -m 755 /opt/tunnelx/fixtures
install -m 644 /opt/tunnelx/validation/test-video.mp4 /opt/tunnelx/fixtures/test-video.mp4
systemd-run --unit=tunnelx-acceptance-fixture --property=User=tunnelx --property=Group=tunnelx /opt/tunnelx/bin/tunnelx-fixture
systemctl is-active tunnelx.service
sha256sum /opt/tunnelx/fixtures/test-video.mp4
ffprobe -v error -show_entries format=duration,size:stream=codec_name,width,height -of json /opt/tunnelx/fixtures/test-video.mp4
