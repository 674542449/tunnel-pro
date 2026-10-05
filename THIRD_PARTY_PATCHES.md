# Vendored changes

Dependencies are pinned in go.mod / go.sum and included with upstream licenses in vendor/.
Builds use the checked-in vendor directory. `go mod vendor` overwrites the following two changes; `tools/patch-vendor.py` reapplies them and verifies the expected source.

* golang.org/x/net/http2 v0.59.0: enable the existing Extended CONNECT implementation by default for this application.
* golang.org/x/net/http2 v0.59.0: mark Authorization and Proxy-Authorization as HPACK sensitive (never-index).

v0.3.3 removes quic-go, qpack, the QPACK patch, and their transitive dependencies. Neither client nor server contains an HTTP/3 transport. H2 UDP Capsules use a local RFC variable-length integer codec with published-vector and truncation tests; this does not require a QUIC library.

No cryptographic primitives are modified. Go TLS 1.3 negotiates its standard AES-GCM / ChaCha20-Poly1305 cipher suites; X25519 is explicitly selected for key exchange. H2 uses operating-system TCP congestion control.

## Bundled node flags

Country/region SVGs: [lipis/flag-icons](https://github.com/lipis/flag-icons), MIT, v7.3.2, commit `fe15c16e7463d0c66d6c5730e9d0e832438d98e1`. The 249 ISO entries in `flags/4x3` are bundled unchanged. Hashes and license: `desktop/ui/flags/manifest.json` and `LICENSE.txt`; redistribution license also ships in `runtime/flag-icons-LICENSE.txt`. Refresh/reproduce with `tools/vendor-node-flags.py`. No runtime CDN requests.
