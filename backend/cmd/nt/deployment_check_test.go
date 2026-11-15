package main

import "testing"

func TestManagementHealthAddress(t *testing.T) {
	for address, want := range map[string]string{"": "http://127.0.0.1:8089/readyz", ":9089": "http://127.0.0.1:9089/readyz", "0.0.0.0:9089": "http://127.0.0.1:9089/readyz", "[::]:9089": "http://[::1]:9089/readyz"} {
		got, err := managementHealthURL(address)
		if err != nil || got != want {
			t.Fatal(address, got, err)
		}
	}
	for _, address := range []string{"http://example.com", "host:8089", ":0", ":65536"} {
		if _, err := managementHealthURL(address); err == nil {
			t.Fatal(address)
		}
	}
}
