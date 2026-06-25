package server

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/saker-ai/assethub/pkg/config"
	commonDiscovery "github.com/saker-ai/saker-common/discovery"
)

func startServiceDiscovery(ctx context.Context, logger *slog.Logger, cfg config.Config) *commonDiscovery.MultiRegistration {
	reg, err := commonDiscovery.StartFromEnv(ctx, serviceInstance("assethub", "AssetHub", cfg.Addr, "/assethub", "/assets", "assethub"), commonDiscovery.EnvOptions{})
	if err != nil {
		logger.WarnContext(ctx, "assethub discovery registration failed", "error", err)
		return nil
	}
	return reg
}

func serviceInstance(id, name, addr, prefix, nativeRoute, audience string) commonDiscovery.ServiceInstance {
	host, port := splitAddr(addr)
	return commonDiscovery.ServiceInstance{ID: id, Name: name, Scheme: "http", Address: host, Port: port, Prefix: prefix, HealthPath: "/healthz", Audience: audience, NativeRoute: nativeRoute}
}

func splitAddr(addr string) (string, int) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		host = "127.0.0.1"
		portText = strings.TrimPrefix(addr, ":")
	} else if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	port, _ := strconv.Atoi(portText)
	return host, port
}
