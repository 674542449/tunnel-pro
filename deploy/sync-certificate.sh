#!/bin/sh
set -eu
domain=$(cat /etc/tunnelx/public-domain)
case "$domain" in *[!A-Za-z0-9.-]*|'') echo 'Invalid domain' >&2; exit 1;; esac
source_dir="/var/lib/caddy/.local/share/caddy/certificates/acme-v02.api.letsencrypt.org-directory/$domain"
dest=/etc/tunnelx/public-cert
if test -f "$dest/current/cert.pem" && cmp -s "$source_dir/$domain.crt" "$dest/current/cert.pem" && cmp -s "$source_dir/$domain.key" "$dest/current/key.pem"; then exit 0; fi
generation="$dest/$(date +%s)-$$"
install -d -m 750 -o root -g tunnelx "$dest" "$generation"
install -m 640 -o root -g tunnelx "$source_dir/$domain.crt" "$generation/cert.pem"
install -m 640 -o root -g tunnelx "$source_dir/$domain.key" "$generation/key.pem"
openssl x509 -in "$generation/cert.pem" -noout -checkhost "$domain" -checkend 3600 >/dev/null
openssl verify -purpose sslserver -verify_hostname "$domain" -untrusted "$generation/cert.pem" "$generation/cert.pem" >/dev/null
cert_public=$(openssl x509 -in "$generation/cert.pem" -pubkey -noout | openssl pkey -pubin -outform DER | openssl dgst -sha256)
key_public=$(openssl pkey -in "$generation/key.pem" -pubout -outform DER | openssl dgst -sha256)
test "$cert_public" = "$key_public" || { echo 'Certificate and private key mismatch' >&2; exit 1; }
ln -s "$generation" "$dest/.current-$$"
mv -Tf "$dest/.current-$$" "$dest/current"
