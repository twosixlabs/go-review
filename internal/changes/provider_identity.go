package changes

import (
	"net/url"
	"strings"
)

func providerHost(u *url.URL) string {
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if u.Scheme == "https" && port == "443" || u.Scheme == "ssh" && port == "22" {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return host
}
