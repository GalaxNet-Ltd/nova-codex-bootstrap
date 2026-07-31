//go:build !darwin && !linux

package agent

import "net"

func verifyPeer(_ net.Conn) error { return nil }
