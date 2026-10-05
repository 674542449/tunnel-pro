#!/bin/sh
set -eu
install -d -m 700 /opt/tunnelx/validation
export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=l
apt-get install -y --no-install-recommends debootstrap ffmpeg >/opt/tunnelx/validation/packages.log 2>&1
if ! test -f /opt/tunnelx/validation/debian13/etc/debian_version; then
    debootstrap --variant=minbase --arch=arm64 trixie /opt/tunnelx/validation/debian13 https://deb.debian.org/debian >/opt/tunnelx/validation/debootstrap.log 2>&1
fi
ffmpeg -hide_banner -loglevel error -f lavfi -i 'testsrc2=size=1280x720:rate=30' -f lavfi -i 'sine=frequency=440:sample_rate=48000' -t 20 -c:v libx264 -preset veryfast -pix_fmt yuv420p -b:v 4M -c:a aac -movflags +faststart -y /opt/tunnelx/validation/test-video.mp4
chmod 644 /opt/tunnelx/validation/test-video.mp4
echo 'Debian root filesystem and controlled video fixture prepared.'
