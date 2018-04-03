package relayServer

import (
	"crypto/rand"
	"fmt"
	"net"
	"sync"

	"github.com/pkg/errors"
	"gitlab.com/pions/pion/pkg/go/stun"
	"golang.org/x/net/ipv4"
)

// Public
type Permission struct {
	IP           net.IP
	TimeToExpiry uint32
}

type Protocol int

const (
	UDP        Protocol = iota
	TCP        Protocol = iota
	TLSOverTCP Protocol = iota
)

type FiveTuple struct {
	SrcIP, DstIP     net.IP
	SrcPort, DstPort int
	Protocol         Protocol
}

func (a *FiveTuple) match(b *FiveTuple) bool {
	return a.SrcIP.Equal(b.SrcIP) &&
		a.DstIP.Equal(b.DstIP) &&
		a.SrcPort == b.SrcPort &&
		a.DstPort == b.DstPort &&
		a.Protocol == b.Protocol
}

func Start(fiveTuple *FiveTuple, reservationToken string, lifetime uint32, username string) (listeningPort int, err error) {
	s := &server{
		FiveTuple:        fiveTuple,
		reservationToken: reservationToken,
		lifetime:         lifetime,
	}

	listener, err := net.ListenPacket("udp", ":0")
	if err != nil {
		return
	}
	_, listeningPort, err = netAddrIPPort(listener.LocalAddr())
	if err != nil {
		return
	}
	s.listeningPort = listeningPort
	s.username = username

	serversLock.Lock()
	servers = append(servers, s)
	serversLock.Unlock()

	go relayHandler(s, listener)
	return
}

//Caller must unlock mutex
func getServer(fiveTuple *FiveTuple) (server *server) {
	serversLock.RLock()

	for _, s := range servers {
		if fiveTuple.match(s.FiveTuple) {
			server = s
		}
	}
	return
}

func Fulfilled(fiveTuple *FiveTuple) bool {
	server := getServer(fiveTuple)
	serversLock.RUnlock()
	return server != nil
}

func AddPermission(fiveTuple *FiveTuple, permission *Permission) error {
	s := getServer(fiveTuple)
	serversLock.RUnlock()
	if s == nil {
		return errors.Errorf("Unable to add permission, server not found")
	}
	s.permissionsLock.Lock()
	s.permissions = append(s.permissions, permission)
	s.permissionsLock.Unlock()
	return nil
}

// Private
type server struct {
	*FiveTuple
	listeningPort              int
	reservationToken, username string
	lifetime                   uint32
	permissionsLock            sync.RWMutex
	permissions                []*Permission
}

var serversLock sync.RWMutex
var servers []*server

const RtpMTU = 1500

func relayHandler(s *server, l net.PacketConn) {
	buffer := make([]byte, RtpMTU)
	conn := ipv4.NewPacketConn(l)
	transactionId := make([]byte, 12)
	destAddr := &net.UDPAddr{IP: s.FiveTuple.SrcIP, Port: s.FiveTuple.SrcPort}

	dataAttr := stun.Data{}
	xorPeerAddressAttr := stun.XorPeerAddress{}

	for {
		n, srcAddr, err := l.ReadFrom(buffer)
		if err != nil {
			fmt.Println("Failing to relay")
		}

		xorPeerAddressAttr.XorAddress.IP = srcAddr.(*net.UDPAddr).IP
		xorPeerAddressAttr.XorAddress.Port = srcAddr.(*net.UDPAddr).Port
		dataAttr.Data = buffer

		rand.Read(transactionId)
		stun.BuildAndSend(conn, destAddr, stun.ClassIndication, stun.MethodData, transactionId, &xorPeerAddressAttr, &dataAttr)
		fmt.Printf("Relaying %s %s %d \n", srcAddr.String(), destAddr.String(), n)
	}
}
