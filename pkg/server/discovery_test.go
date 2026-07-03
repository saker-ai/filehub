package server

import "testing"

func TestServiceInstanceUsesWebHubRoutes(t *testing.T) {
	got := serviceInstance("filehub", "FileHub", ":17040", "/filehub", "/assets", "filehub")

	if got.ID != "filehub" || got.Name != "FileHub" || got.Scheme != "http" {
		t.Fatalf("identity = %#v", got)
	}
	if got.Address != "127.0.0.1" || got.Port != 17040 {
		t.Fatalf("address = %s:%d", got.Address, got.Port)
	}
	if got.Prefix != "/filehub" || got.NativeRoute != "/assets" || got.HealthPath != "/healthz" || got.Audience != "filehub" {
		t.Fatalf("routes = %#v", got)
	}
}

func TestSplitAddrNormalizesWildcardHosts(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		wantHost string
		wantPort int
	}{
		{name: "port only", addr: ":17040", wantHost: "127.0.0.1", wantPort: 17040},
		{name: "wildcard ipv4", addr: "0.0.0.0:17041", wantHost: "127.0.0.1", wantPort: 17041},
		{name: "wildcard ipv6", addr: "[::]:17042", wantHost: "127.0.0.1", wantPort: 17042},
		{name: "specific host", addr: "10.0.0.8:17043", wantHost: "10.0.0.8", wantPort: 17043},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port := splitAddr(tt.addr)
			if host != tt.wantHost || port != tt.wantPort {
				t.Fatalf("splitAddr(%q) = %s:%d, want %s:%d", tt.addr, host, port, tt.wantHost, tt.wantPort)
			}
		})
	}
}
