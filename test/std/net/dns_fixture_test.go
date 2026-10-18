package net_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

const dnsFixtureHost = "fixture.invalid."

// Use a private UDP server so record lookups test the resolver independently
// of the host's DNS configuration or public network access.
func localDNSResolver(t *testing.T) *net.Resolver {
	t.Helper()
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		buffer := make([]byte, 1500)
		for {
			n, peer, err := server.ReadFrom(buffer)
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					done <- err
				} else {
					done <- nil
				}
				return
			}
			reply, err := dnsFixtureReply(buffer[:n])
			if err == nil {
				_, err = server.WriteTo(reply, peer)
			}
			if err != nil {
				done <- err
				return
			}
		}
	}()
	t.Cleanup(func() {
		server.Close()
		if err := <-done; err != nil {
			t.Errorf("DNS fixture: %v", err)
		}
	})
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "udp4", server.LocalAddr().String())
	}}
}

func useLocalDNS(t *testing.T) *net.Resolver {
	t.Helper()
	r := localDNSResolver(t)
	previous := net.DefaultResolver
	net.DefaultResolver = r
	t.Cleanup(func() { net.DefaultResolver = previous })
	return r
}

func dnsFixtureName(name string) []byte {
	var data []byte
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		data = append(data, byte(len(label)))
		data = append(data, label...)
	}
	return append(data, 0)
}

func dnsFixtureReply(query []byte) ([]byte, error) {
	if len(query) < 17 || binary.BigEndian.Uint16(query[4:6]) != 1 {
		return nil, fmt.Errorf("invalid DNS question: %x", query)
	}
	end := 12
	var labels []string
	for {
		if end >= len(query) {
			return nil, fmt.Errorf("truncated DNS name")
		}
		n := int(query[end])
		end++
		if n == 0 {
			break
		}
		if n > 63 || end+n >= len(query) {
			return nil, fmt.Errorf("invalid DNS label")
		}
		labels = append(labels, string(query[end:end+n]))
		end += n
	}
	if end+4 > len(query) {
		return nil, fmt.Errorf("truncated DNS question")
	}
	kind := binary.BigEndian.Uint16(query[end : end+2])
	end += 4
	wantName := dnsFixtureHost
	if kind == 33 {
		wantName = "_xmpp-server._tcp." + dnsFixtureHost
	}
	if name := strings.Join(labels, ".") + "."; name != wantName {
		return nil, fmt.Errorf("DNS question name = %q, want %q", name, wantName)
	}
	var record []byte
	switch kind {
	case 1: // A
		record = []byte{192, 0, 2, 10}
	case 28: // AAAA
		record = net.ParseIP("2001:db8::10").To16()
	case 16: // TXT: the resolver joins the character strings in one record.
		record = []byte{4, 't', 'e', 's', 't', 6, 'r', 'e', 'c', 'o', 'r', 'd'}
	case 15: // MX
		record = append([]byte{0, 10}, dnsFixtureName("mail.fixture.invalid.")...)
	case 2: // NS
		record = dnsFixtureName("ns.fixture.invalid.")
	case 33: // SRV
		record = append([]byte{0, 10, 0, 20, 0x10, 0x92}, dnsFixtureName("service.fixture.invalid.")...)
	default:
		return nil, fmt.Errorf("unexpected DNS type %d", kind)
	}
	reply := []byte{query[0], query[1], 0x81, 0x80, 0, 1, 0, 1, 0, 0, 0, 0}
	reply = append(reply, query[12:end]...)
	// The answer owner points to the original question name at offset 12.
	reply = append(reply, 0xc0, 0x0c, byte(kind>>8), byte(kind), 0, 1, 0, 0, 0, 0, byte(len(record)>>8), byte(len(record)))
	return append(reply, record...), nil
}
