package mux

import (
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"tunnel/internal/proto"

	"github.com/gorilla/websocket"
)

const maxPayload = 16 * 1024

// Stream is one logical connection inside a Mux.
type Stream struct {
	id      uint32
	m       *Mux
	readCh  chan []byte
	partial []byte
	done    chan struct{}
	once    sync.Once
}

func (s *Stream) Read(p []byte) (int, error) {
	if len(s.partial) > 0 {
		n := copy(p, s.partial)
		s.partial = s.partial[n:]
		return n, nil
	}
	select {
	case data, ok := <-s.readCh:
		if !ok {
			return 0, io.EOF
		}
		n := copy(p, data)
		if n < len(data) {
			s.partial = data[n:]
		}
		return n, nil
	case <-s.done:
		return 0, io.EOF
	}
}

func (s *Stream) Write(p []byte) (int, error) {
	sent := 0
	for sent < len(p) {
		end := sent + maxPayload
		if end > len(p) {
			end = len(p)
		}
		if err := s.m.writeFrame(proto.MakeFrame(s.id, proto.CmdData, p[sent:end])); err != nil {
			return sent, err
		}
		sent = end
	}
	return sent, nil
}

func (s *Stream) Close() error {
	s.once.Do(func() {
		close(s.done)
		s.m.writeFrame(proto.MakeFrame(s.id, proto.CmdFin, nil))
	})
	s.m.streams.Delete(s.id)
	return nil
}

func (s *Stream) SendAck(status byte) error {
	return s.m.writeFrame(proto.MakeFrame(s.id, proto.CmdAck, []byte{status}))
}

func (s *Stream) UserID() string { return s.m.UserID() }

// Mux multiplexes many Streams over one WebSocket.
type Mux struct {
	ws      *websocket.Conn
	streams sync.Map
	nextID  atomic.Uint32
	writeMu sync.Mutex
	closed  atomic.Bool
	doneCh  chan struct{}
	userID  atomic.Value // string
}

func (m *Mux) UserID() string {
	if v := m.userID.Load(); v != nil {
		return v.(string)
	}
	return ""
}

func (m *Mux) SetUserID(uid string) { m.userID.Store(uid) }

func (m *Mux) SendUserID(uid string) error {
	return m.writeFrame(proto.MakeFrame(0, proto.CmdUserID, []byte(uid)))
}

func (m *Mux) writeFrame(data []byte) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return m.ws.WriteMessage(websocket.BinaryMessage, data)
}

func (m *Mux) newStream(id uint32) *Stream {
	s := &Stream{
		id:     id,
		m:      m,
		readCh: make(chan []byte, 128),
		done:   make(chan struct{}),
	}
	m.streams.Store(id, s)
	return s
}

func (m *Mux) deliver(id uint32, data []byte) {
	v, ok := m.streams.Load(id)
	if !ok {
		return
	}
	s := v.(*Stream)
	buf := make([]byte, len(data))
	copy(buf, data)
	select {
	case s.readCh <- buf:
	case <-s.done:
	}
}

func (m *Mux) IsClosed() bool {
	return m.closed.Load()
}

func (m *Mux) Close() error {
	if m.closed.CompareAndSwap(false, true) {
		return m.ws.Close()
	}
	return nil
}

// ---------- client side ----------

func NewClientMux(ws *websocket.Conn, psk string) (*Mux, error) {
	m := &Mux{ws: ws, doneCh: make(chan struct{})}
	m.nextID.Store(1)

	if err := m.writeFrame(proto.MakeFrame(0, proto.CmdAuth, proto.MakeAuthPayload(psk))); err != nil {
		return nil, fmt.Errorf("send auth: %w", err)
	}

	_, raw, err := ws.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read auth ack: %w", err)
	}
	_, cmd, payload, err := proto.ParseFrame(raw)
	if err != nil || cmd != proto.CmdAck || len(payload) < 1 || payload[0] != proto.StatusOK {
		return nil, errors.New("auth rejected")
	}

	go m.clientReadLoop()
	go m.keepalive()
	go m.trafficNoise()
	return m, nil
}

func (m *Mux) keepalive() {
	for {
		time.Sleep(time.Duration(30+rand.Intn(60)) * time.Second)
		if m.closed.Load() {
			return
		}
		m.writeFrame(proto.MakeFrame(0, proto.CmdPing, nil))
	}
}

func (m *Mux) trafficNoise() {
	time.Sleep(time.Duration(10+rand.Intn(20)) * time.Second)
	for {
		if m.closed.Load() {
			return
		}
		n := 1 + rand.Intn(3)
		for i := 0; i < n; i++ {
			if m.closed.Load() {
				return
			}
			size := 32 + rand.Intn(128)
			m.writeFrame(proto.MakeFrame(0, proto.CmdPing, make([]byte, size)))
			time.Sleep(time.Duration(200+rand.Intn(600)) * time.Millisecond)
		}
		time.Sleep(time.Duration(45+rand.Intn(75)) * time.Second)
	}
}

func (m *Mux) clientReadLoop() {
	defer func() {
		m.closed.Store(true)
		close(m.doneCh)
		m.streams.Range(func(key, value any) bool {
			s := value.(*Stream)
			s.once.Do(func() { close(s.done) })
			return true
		})
	}()

	for {
		_, raw, err := m.ws.ReadMessage()
		if err != nil {
			return
		}
		sid, cmd, payload, err := proto.ParseFrame(raw)
		if err != nil {
			continue
		}
		switch cmd {
		case proto.CmdData:
			m.deliver(sid, payload)
		case proto.CmdAck:
			m.deliver(sid, payload)
		case proto.CmdFin:
			if v, ok := m.streams.LoadAndDelete(sid); ok {
				s := v.(*Stream)
				close(s.readCh)
			}
		}
	}
}

func (m *Mux) OpenStream(host string, port uint16) (*Stream, error) {
	if m.closed.Load() {
		return nil, errors.New("mux closed")
	}

	id := m.nextID.Add(1) - 1
	s := m.newStream(id)

	if err := m.writeFrame(proto.MakeFrame(id, proto.CmdOpen, proto.MakeConnectPayload(host, port))); err != nil {
		m.streams.Delete(id)
		m.closed.Store(true)
		return nil, err
	}

	select {
	case ack, ok := <-s.readCh:
		if !ok {
			m.streams.Delete(id)
			return nil, errors.New("stream closed")
		}
		if len(ack) < 1 || ack[0] != proto.StatusOK {
			m.streams.Delete(id)
			return nil, fmt.Errorf("rejected %s:%d", host, port)
		}
	case <-s.done:
		m.streams.Delete(id)
		return nil, errors.New("mux closed")
	case <-time.After(15 * time.Second):
		m.streams.Delete(id)
		return nil, errors.New("open timeout")
	}

	return s, nil
}

// ---------- server side ----------

type StreamHandler func(s *Stream, target string)

func ServeConn(ws *websocket.Conn, psk string, handler StreamHandler) error {
	m := &Mux{ws: ws, doneCh: make(chan struct{})}

	_, raw, err := ws.ReadMessage()
	if err != nil {
		return err
	}
	_, cmd, payload, err := proto.ParseFrame(raw)
	if err != nil || cmd != proto.CmdAuth {
		return errors.New("expected auth")
	}
	if err := proto.VerifyAuthPayload(payload, psk); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	if err := m.writeFrame(proto.MakeFrame(0, proto.CmdAck, []byte{proto.StatusOK})); err != nil {
		return err
	}
	log.Println("[mux] authenticated")

	defer func() {
		m.closed.Store(true)
		close(m.doneCh)
		m.streams.Range(func(key, value any) bool {
			s := value.(*Stream)
			s.once.Do(func() { close(s.done) })
			return true
		})
	}()

	for {
		_, raw, err := m.ws.ReadMessage()
		if err != nil {
			return err
		}
		sid, cmd, payload, err := proto.ParseFrame(raw)
		if err != nil {
			continue
		}

		switch cmd {
		case proto.CmdOpen:
			target, err := proto.ParseConnectPayload(payload)
			if err != nil {
				m.writeFrame(proto.MakeFrame(sid, proto.CmdAck, []byte{proto.StatusFail}))
				continue
			}
			s := m.newStream(sid)
			go handler(s, target)

		case proto.CmdData:
			m.deliver(sid, payload)

		case proto.CmdFin:
			if v, ok := m.streams.LoadAndDelete(sid); ok {
				s := v.(*Stream)
				close(s.readCh)
			}

		case proto.CmdPing:

		case proto.CmdUserID:
			if len(payload) > 0 {
				m.SetUserID(string(payload))
				log.Printf("[mux] user_id=%s", string(payload))
			}
		}
	}
}
