package binding

import (
	"errors"
	"io"
	"sync"
	"time"
)

// ErrNotConnected is returned when a data-plane operation is attempted before
// Setup succeeded or after Close.
var ErrNotConnected = errors.New("vpn client is not connected")

// ErrTimeout is returned by ReadPacket when no packet arrives in time.
var ErrTimeout = errors.New("read packet timeout")

// ErrClosed is returned when the session was closed while an operation was in
// progress.
var ErrClosed = errors.New("vpn client is closed")

// maxPacketSize is the largest IP packet the tunnel can deliver. The MTU is
// 1400 in practice, but IPv4 packets may legitimately be up to 65535 bytes.
const maxPacketSize = 65536

// packetStream decouples the blocking tunnel read from the host-facing
// ReadPacket timeout. A single goroutine reads the tunnel and enqueues
// self-contained packets, so a timed-out host read never corrupts the stream.
type packetStream struct {
	conn io.ReadWriteCloser

	ch        chan []byte
	done      chan struct{}
	closeOnce sync.Once

	errMu sync.Mutex
	err   error
}

func newPacketStream(conn io.ReadWriteCloser) *packetStream {
	stream := &packetStream{
		conn: conn,
		ch:   make(chan []byte, 256),
		done: make(chan struct{}),
	}
	go stream.readLoop()
	return stream
}

func (s *packetStream) readLoop() {
	defer close(s.ch)
	buf := make([]byte, maxPacketSize)
	for {
		n, err := s.conn.Read(buf)
		if n > 0 {
			packet := make([]byte, n)
			copy(packet, buf[:n])
			select {
			case s.ch <- packet:
			case <-s.done:
				return
			}
		}
		if err != nil {
			s.setErr(err)
			return
		}
	}
}

func (s *packetStream) setErr(err error) {
	s.errMu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.errMu.Unlock()
}

func (s *packetStream) Err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.err == nil {
		return io.EOF
	}
	return s.err
}

func (s *packetStream) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		_ = s.conn.Close()
	})
}

// ensureL3 lazily opens the L3 connection and wraps it in a packet stream.
func (c *Client) ensureL3() (*packetStream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.l3Conn != nil {
		return c.l3Conn, nil
	}
	if c.vpnClient == nil {
		return nil, ErrNotConnected
	}
	conn, err := c.vpnClient.NewL3Conn()
	if err != nil {
		return nil, err
	}
	stream := newPacketStream(conn)
	c.l3Conn = stream
	return stream, nil
}

// ReadPacket returns the next IPv4 packet from the VPN server.
//
// The packet is copied into buf, which must be large enough to hold it
// (65536 bytes is always sufficient; 1500 covers a normal MTU). A timeout of
// zero blocks indefinitely.
//
// It is safe to call ReadPacket from a single goroutine; concurrent calls are
// serialised but packets may arrive out of order for the callers.
func (c *Client) ReadPacket(buf []byte, timeout time.Duration) (int, error) {
	stream, err := c.ensureL3()
	if err != nil {
		return 0, err
	}

	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	select {
	case packet, ok := <-stream.ch:
		if !ok {
			return 0, stream.Err()
		}
		if len(buf) < len(packet) {
			return 0, io.ErrShortBuffer
		}
		return copy(buf, packet), nil
	case <-timeoutCh:
		return 0, ErrTimeout
	case <-c.closed:
		return 0, ErrClosed
	}
}

// WritePacket sends one IPv4 packet to the VPN server.
func (c *Client) WritePacket(packet []byte) error {
	stream, err := c.ensureL3()
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	select {
	case <-c.closed:
		return ErrClosed
	default:
	}
	_, err = stream.conn.Write(packet)
	return err
}
