//go:build !windows

package processowner

import (
	"net"
	"tunnelx/internal/diagnostics"
)

func Lookup(peer, proxy net.Addr) diagnostics.Owner {
	return diagnostics.Owner{Error: "unsupported_platform"}
}
