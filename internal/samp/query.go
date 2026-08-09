package samp

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strconv"
	"time"
)

type Info struct {
	Password                     bool
	Players, MaxPlayers          int
	Hostname, GameMode, Language string
}

const (
	protocolMagic          = "SAMP"
	queryNetwork           = "udp4"
	queryOpcode       byte = 'i'
	dialTimeout            = 3 * time.Second
	queryTimeout           = 4 * time.Second
	queryHeaderSize        = 11
	queryOpcodeOffset      = 10
	maxResponseSize        = 4096
	uint16Size             = 2
	uint32Size             = 4
)

func Query(ctx context.Context, host string, port int) (Info, error) {
	ip := net.ParseIP(host)
	if ip == nil {
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
		if err != nil || len(ips) == 0 {
			return Info{}, errors.New("cannot resolve server")
		}
		ip = ips[0]
	}
	ip = ip.To4()
	if ip == nil {
		return Info{}, errors.New("IPv4 is required")
	}
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	c, err := net.DialTimeout(queryNetwork, addr, dialTimeout)
	if err != nil {
		return Info{}, err
	}
	defer c.Close()
	deadline := time.Now().Add(queryTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetDeadline(deadline)
	p := append([]byte(protocolMagic), ip...)
	var pb [2]byte
	binary.LittleEndian.PutUint16(pb[:], uint16(port))
	p = append(p, pb[:]...)
	p = append(p, queryOpcode)
	if _, err = c.Write(p); err != nil {
		return Info{}, err
	}
	buf := make([]byte, maxResponseSize)
	n, err := c.Read(buf)
	if err != nil {
		return Info{}, err
	}
	if n < queryHeaderSize || string(buf[:len(protocolMagic)]) != protocolMagic || buf[queryOpcodeOffset] != queryOpcode {
		return Info{}, errors.New("invalid SA-MP query response")
	}
	r := reader{b: buf[queryHeaderSize:n]}
	out := Info{}
	var pass byte
	if !r.read(&pass) || !r.read16(&out.Players) || !r.read16(&out.MaxPlayers) {
		return Info{}, errors.New("truncated SA-MP response")
	}
	out.Password = pass != 0
	if !r.str(&out.Hostname) || !r.str(&out.GameMode) || !r.str(&out.Language) {
		return Info{}, errors.New("truncated SA-MP strings")
	}
	return out, nil
}

type reader struct {
	b []byte
	p int
}

func (r *reader) read(v *byte) bool {
	if r.p >= len(r.b) {
		return false
	}
	*v = r.b[r.p]
	r.p++
	return true
}
func (r *reader) read16(v *int) bool {
	if r.p+uint16Size > len(r.b) {
		return false
	}
	*v = int(binary.LittleEndian.Uint16(r.b[r.p:]))
	r.p += uint16Size
	return true
}
func (r *reader) str(v *string) bool {
	if r.p+uint32Size > len(r.b) {
		return false
	}
	n := int(binary.LittleEndian.Uint32(r.b[r.p:]))
	r.p += uint32Size
	if n < 0 || r.p+n > len(r.b) {
		return false
	}
	*v = string(r.b[r.p : r.p+n])
	r.p += n
	return true
}
