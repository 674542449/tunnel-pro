//go:build !windows

package desktop

import "errors"

func protect([]byte) ([]byte, error) {
	return nil, errors.New("desktop credential encryption requires Windows")
}
func unprotect([]byte) ([]byte, error) {
	return nil, errors.New("desktop credential encryption requires Windows")
}
