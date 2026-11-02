package main

import "testing"

func TestListenAddress(t *testing.T) {
	for _, c := range []struct {
		name, addr, port string
		dev              bool
		want             string
		fails            bool
	}{
		{"nothing set", "", "", false, ":8080", false},
		{"a hosting platform's port", "", "10000", false, ":10000", false},
		{"explicit address wins", "127.0.0.1:9", "10000", false, "127.0.0.1:9", false},
		{"dev mode stays on this machine", "", "", true, "127.0.0.1:8080", false},
		{"dev mode on purpose elsewhere", ":8080", "", true, ":8080", false},
		{"dev mode on a hosted deploy is refused", "", "10000", true, "", true},
		{"dev mode with a port but an explicit address is allowed", "127.0.0.1:9", "10000", true, "127.0.0.1:9", false},
	} {
		got, err := listenAddress(c.addr, c.port, c.dev)
		if (err != nil) != c.fails || got != c.want {
			t.Errorf("%s: got %q, %v; want %q (fails %v)", c.name, got, err, c.want, c.fails)
		}
	}
}

func TestIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8080": true, "[::1]:8080": true, "localhost:8080": true,
		":8080": false, "0.0.0.0:8080": false, "10.0.0.5:8080": false, "bad": false,
	} {
		if got := isLoopback(addr); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}
