package ring

import (
	"encoding/binary"
	"errors"
	"log"
	"math"
	"net"
	"sync"
	"time"
)

var (
	DefaultChunksize int           = 1024 * 16       // 16Kb
	DefaultTimeout   time.Duration = 2 * time.Second // 2 seconds
)

const (
	DefaultAddress = iota
)

type RingConn struct {
	Conn   net.Conn
	Writer *TimeoutWriter
	sync.Mutex
}

func NewRingConn(conn net.Conn) *RingConn {
	return &RingConn{
		Conn:   conn,
		Writer: NewTimeoutWriter(conn),
	}
}

type TCPMsgRing struct {
	ring        *Ring
	msgHandlers map[uint64]MsgUnmarshaller
	conns       map[string]*RingConn
	AddressIdx  uint // Set this to use a different node address
}

func NewTCPMsgRing(r *Ring) *TCPMsgRing {
	return &TCPMsgRing{
		ring:        r,
		msgHandlers: make(map[uint64]MsgUnmarshaller),
		conns:       make(map[string]*RingConn),
		AddressIdx:  DefaultAddress,
	}
}

func (m *TCPMsgRing) Ring() *Ring {
	return m.ring
}

func (m *TCPMsgRing) GetNodesForPart(ringVersion int64, partition uint32) []uint64 {
	// Just a dummy function for now
	return []uint64{uint64(1), uint64(2)}
}

func (m *TCPMsgRing) MaxMsgLength() uint64 {
	return math.MaxUint64
}

func (m *TCPMsgRing) SetMsgHandler(msgType uint64, handler MsgUnmarshaller) {
	m.msgHandlers[uint64(msgType)] = handler
}

func (m *TCPMsgRing) MsgToNode(nodeID uint64, msg Msg) {
	m.msgToNode(nodeID, msg)
	msg.Done()
}

func (m *TCPMsgRing) msgToNode(nodeID uint64, msg Msg) {
	// TODO: Add retry functionality
	n := m.ring.Node(nodeID)
	if n == nil {
		return
	}
	// See if we have a connection already
	// TODO: This whole thing should be configurable to use a given "slot" in
	// the Addresses list.
	conn, ok := m.conns[n.Addresses[m.AddressIdx]]
	if !ok {
		// We need to open a connection
		// TODO: Handle connection timeouts
		tcpconn, err := net.DialTimeout("tcp", n.Addresses[m.AddressIdx], DefaultTimeout)
		if err != nil {
			log.Println("ERR: Trying to connect to", n.Addresses[m.AddressIdx], err)
			return
		}
		conn := NewRingConn(tcpconn)
		m.conns[n.Addresses[m.AddressIdx]] = conn
	}
	conn.Lock() // Make sure we only have one writer at a time
	// TODO: Handle write timeouts
	// write the msg type
	msgType := msg.MsgType()
	for i := uint(0); i <= 56; i += 8 {
		_ = conn.Writer.WriteByte(byte(msgType >> i))
	}
	// Write the msg size
	msgLength := msg.MsgLength()
	for i := uint(0); i <= 56; i += 8 {
		_ = conn.Writer.WriteByte(byte(msgLength >> i))
	}
	// Write the msg
	length, err := msg.WriteContent(conn.Writer)
	// Make sure we flush the data
	conn.Writer.Flush()
	conn.Unlock()
	if err != nil {
		log.Println("ERR: Sending content - ", err)
		return
	}
	if length != msg.MsgLength() {
		log.Println("ERR: Didn't send enough data", length, msg.MsgLength())
		return
	}
}

func (m *TCPMsgRing) MsgToNodeChan(nodeID uint64, msg Msg, retchan chan struct{}) {
	m.msgToNode(nodeID, msg)
	retchan <- struct{}{}
}

func (m *TCPMsgRing) MsgToOtherReplicas(ringVersion int64, partition uint32, msg Msg) {
	nodes := m.GetNodesForPart(ringVersion, partition)
	retchan := make(chan struct{}, 2)
	for _, nodeID := range nodes {
		go m.MsgToNodeChan(nodeID, msg, retchan)
	}
	for i := 0; i < len(nodes); i++ {
		<-retchan
	}
	msg.Done()
}

func (m *TCPMsgRing) handle(conn net.Conn) error {
	reader := NewTimeoutReader(conn)
	var length uint64
	var msgType uint64
	for {
		// for v.00002 we will store this in the fist 8 bytes
		err := binary.Read(reader, binary.LittleEndian, &msgType)
		if err != nil {
			log.Println("Closing connection")
			conn.Close()
			return err
		}
		handle, ok := m.msgHandlers[msgType]
		if !ok {
			log.Println("ERR: Unknown message type", msgType)
			// TODO: Handle errors better
			log.Println("Closing connection")
			conn.Close()
			return errors.New("Unknown message type")
		}
		// for v.00002 the msg length will be the next 8 bytes
		err = binary.Read(reader, binary.LittleEndian, &length)
		if err != nil {
			log.Println("ERR: Error reading length")
			// TODO: Handle errors better
			log.Println("Closing connection")
			conn.Close()
			return err
		}
		// attempt to handle the message
		consumed, err := handle(reader, length)
		if err != nil {
			log.Println("ERR: Error handling message", err)
			// TODO: Handle errors better
			log.Println("Closing connection")
			conn.Close()
			return err
		}
		if consumed != length {
			log.Println("ERR: Didn't consume whole message", length, consumed)
			// TODO: Handle errors better
			log.Println("Closing connection")
			conn.Close()
			return errors.New("Didn't consume whole message")
		}
	}
}

func (m *TCPMsgRing) Listen(addr string) error {
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return err
	}
	server, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return err
	}
	for {
		conn, err := server.AcceptTCP()
		if err != nil {
			// TODO: Not sure what types of errors occur here
			log.Println("Err accepting conn:", err)
			continue
		}
		go m.handle(conn)
	}
}