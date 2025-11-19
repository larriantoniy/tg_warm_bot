package tg

import (
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/larriantoniy/tg_user_bot/internal/ports"
)

func isIPv6Literal(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() == nil // есть IP и это не IPv4 → IPv6
}

func isIPv4Literal(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() != nil
}

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

	host := proxyCfg.Server
	port := proxyCfg.Port

	// IPv6 literal
	if isIPv6Literal(host) {
		addr := fmt.Sprintf("[%s]:%d", host, port)
		logger.Info("checking IPv6 proxy...", "addr", addr)

		conn, err := net.DialTimeout("tcp6", addr, 5*time.Second)
		if err != nil {
			logger.Error("IPv6 proxy unreachable", "addr_v6", addr, "error_v6", err)
			return
		}
		_ = conn.Close()
		logger.Info("proxy reachable on IPv6", "addr_v6", addr)
		return
	}

	// IPv4 literal
	if isIPv4Literal(host) {
		addr4 := fmt.Sprintf("%s:%d", host, port)
		logger.Info("checking IPv4 proxy...", "addr", addr4)

		conn4, err4 := net.DialTimeout("tcp4", addr4, 5*time.Second)
		if err4 != nil {
			logger.Error("IPv4 proxy unreachable", "addr_v4", addr4, "error_v4", err4)
			return
		}
		_ = conn4.Close()
		logger.Info("proxy reachable on IPv4", "addr_v4", addr4)
		return
	}

	// Иначе – hostname: пробуем сначала IPv6, потом IPv4
	addr6 := fmt.Sprintf("[%s]:%d", host, port)
	logger.Info("checking proxy via hostname, IPv6 first...", "addr_v6", addr6)

	if conn6, err6 := net.DialTimeout("tcp6", addr6, 5*time.Second); err6 == nil {
		_ = conn6.Close()
		logger.Info("proxy reachable on IPv6 via hostname", "addr_v6", addr6)
		return
	} else {
		logger.Warn("proxy IPv6 via hostname failed, trying IPv4", "error_v6", err6)
	}

	addr4 := fmt.Sprintf("%s:%d", host, port)
	if conn4, err4 := net.DialTimeout("tcp4", addr4, 5*time.Second); err4 == nil {
		_ = conn4.Close()
		logger.Info("proxy reachable on IPv4 via hostname", "addr_v4", addr4)
		return
	} else {
		logger.Error("proxy unreachable via hostname on both IPv6 and IPv4",
			"addr_v6", addr6, "addr_v4", addr4, "error_v4", err4)
	}
}
