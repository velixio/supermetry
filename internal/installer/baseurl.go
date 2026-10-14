package installer

import (
	"errors"
	"net"
	"strings"
)

// suggestBaseURL is the default public URL for a listen address. Listening on all
// interfaces suggests this machine's address so other devices can reach it.
func suggestBaseURL(listen string, localIP func() (string, error)) string {
	host, port, _ := strings.Cut(listen, ":")
	if host == "0.0.0.0" {
		host = "127.0.0.1"
		if ip, err := localIP(); err == nil && ip != "" {
			host = ip
		}
	}
	return "http://" + host + ":" + port
}

// detectLocalIP returns the IPv4 address of the interface that carries outbound
// traffic, falling back to the first non-loopback interface address. The UDP dial
// sends no packets; it only asks the kernel which source address it would use.
func detectLocalIP() (string, error) {
	if conn, err := net.Dial("udp4", "192.0.2.1:9"); err == nil {
		addr, ok := conn.LocalAddr().(*net.UDPAddr)
		_ = conn.Close()
		if ok && usableIPv4(addr.IP) {
			return addr.IP.String(), nil
		}
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && usableIPv4(ipNet.IP) {
			return ipNet.IP.String(), nil
		}
	}
	return "", errors.New("no non-loopback IPv4 address found")
}

func usableIPv4(ip net.IP) bool {
	return ip.To4() != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}
