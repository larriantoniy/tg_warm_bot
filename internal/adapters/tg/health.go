package tg

import (
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/larriantoniy/tg_user_bot/internal/ports"
)

// IPv4 check
func checkIPv4(logger *slog.Logger) {
	logger.Info("checking IPv4 connectivity...")

	conn, err := net.DialTimeout("tcp4", "8.8.8.8:53", 3*time.Second)
	if err != nil {
		logger.Warn("IPv4 seems not working", "error", err)
		return
	}
	_ = conn.Close()

	logger.Info("IPv4 OK")
}

// IPv6 check
func checkIPv6(logger *slog.Logger) {
	logger.Info("checking IPv6 connectivity...")

	conn, err := net.DialTimeout("tcp6", "[2606:4700:4700::1111]:53", 3*time.Second)
	if err != nil {
		logger.Warn("IPv6 seems not working", "error", err)
		return
	}
	_ = conn.Close()

	logger.Info("IPv6 OK")
}

func checkProxy(logger *slog.Logger, proxyCfg *ports.ProxyConfig) {
	if proxyCfg == nil || !proxyCfg.Enabled {
		logger.Info("proxy disabled, skipping check")
		return
	}

	addr := fmt.Sprintf("[%s]:%d", proxyCfg.Server, proxyCfg.Port)

	logger.Info("checking IPv6 proxy...", "addr", addr)

	conn, err := net.DialTimeout("tcp6", addr, 5*time.Second)
	if err != nil {
		logger.Warn("IPv6 proxy unreachable, trying IPv4...", "error", err)

		addr4 := fmt.Sprintf("%s:%d", proxyCfg.Server, proxyCfg.Port)
		conn4, err4 := net.DialTimeout("tcp4", addr4, 5*time.Second)
		if err4 != nil {
			logger.Error("proxy unreachable on both IPv6 and IPv4",
				"addr_v6", addr,
				"addr_v4", addr4,
				"error_v6", err,
				"error_v4", err4,
			)
			return
		}

		_ = conn4.Close()

		logger.Info("proxy reachable on IPv4", "addr", addr4)
		return
	}

	_ = conn.Close()
	logger.Info("proxy reachable on IPv6", "addr", addr)
}
