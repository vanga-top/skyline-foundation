package quic

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/vanga-top/skyline-foundation/quicserver/quic/internal/protocol"
	"github.com/vanga-top/skyline-foundation/quicserver/quic/logging"
	"github.com/vanga-top/skyline-foundation/quicserver/quic/utils"
	"hash"
	"log"
	"net"
	"sync"
	"time"
)

type packetHandlerMap struct {
	mutex       sync.Mutex
	conn        connection
	connIDLen   int
	handlers    map[string]packetHandler //key connectionID
	resetTokens map[protocol.StatelessResetToken]packetHandler
	server      unknownPacketHandler

	listening chan struct{}
	closed    bool

	deleteRetiredSessionsAfter time.Duration

	statelessResetEnabled bool
	statelessResetMutex   sync.Mutex
	statelessResetHasher  hash.Hash

	tracer logging.Tracer
	logger utils.Logger
}

func (h *packetHandlerMap) AddWithConnID(clientDestConnID protocol.ConnectionID, newConnID protocol.ConnectionID, fn func() packetHandler) bool {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	if _, ok := h.handlers[string(clientDestConnID)]; ok {
		h.logger.Debugf("Not adding connection ID %s for a new session, as it already exists.", clientDestConnID)
		return false
	}

	sess := fn()
	h.handlers[string(clientDestConnID)] = sess
	h.handlers[string(newConnID)] = sess
	h.logger.Debugf("Adding connection IDs %s and %s for a new session.", clientDestConnID, newConnID)
	return true
}

func (h *packetHandlerMap) Destroy() error {
	if err := h.conn.Close(); err != nil {
		return err
	}
	<-h.listening // wait until listening returns
	return nil
}

func (h *packetHandlerMap) Add(id protocol.ConnectionID, handler packetHandler) bool {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	if _, ok := h.handlers[string(id)]; ok {
		h.logger.Debugf("Not adding connection ID %s, as it already exists.", id)
		return false
	}
	h.handlers[string(id)] = handler
	h.logger.Debugf("Adding connection ID %s", id)
	return true
}

func (h *packetHandlerMap) GetStatelessResetToken(id protocol.ConnectionID) protocol.StatelessResetToken {
	panic("implement me")
}

func (h *packetHandlerMap) Retry(id protocol.ConnectionID) {
	h.logger.Debugf("Retiring connection ID %s in %s.", id, h.deleteRetiredSessionsAfter)
	time.AfterFunc(h.deleteRetiredSessionsAfter, func() {
		h.mutex.Lock()
		delete(h.handlers, string(id))
		h.mutex.Unlock()
		h.logger.Debugf("Removing connection ID %s after it has been retired.", id)
	})
}

func (h *packetHandlerMap) Remove(id protocol.ConnectionID) {
	h.mutex.Lock()
	delete(h.handlers, string(id))
	h.mutex.Unlock()
	h.logger.Debugf("Removing connection ID %s.", id)
}

func (h *packetHandlerMap) ReplaceWithClosed(id protocol.ConnectionID, handler packetHandler) {
	h.mutex.Lock()
	h.handlers[string(id)] = handler
	h.mutex.Unlock()
	h.logger.Debugf("Replacing session for connection ID %s with a closed session.", id)

	time.AfterFunc(h.deleteRetiredSessionsAfter, func() {
		h.mutex.Lock()
		handler.shutdown()
		delete(h.handlers, string(id))
		h.mutex.Unlock()
		h.logger.Debugf("Removing connection ID %s for a closed session after it has been retired.", id)
	})
}

func (h *packetHandlerMap) AddResetToken(token protocol.StatelessResetToken, handler packetHandler) {
	h.mutex.Lock()
	h.resetTokens[token] = handler
	h.mutex.Unlock()
}

func (h *packetHandlerMap) RemoveResetToken(token protocol.StatelessResetToken) {
	h.mutex.Lock()
	delete(h.resetTokens, token)
	h.mutex.Unlock()
}

func (h *packetHandlerMap) SetServer(handler unknownPacketHandler) {
	h.mutex.Lock()
	h.server = handler
	h.mutex.Unlock()
}

func (h *packetHandlerMap) CloseServer() {
	h.mutex.Lock()
	if h.server == nil {
		h.mutex.Unlock()
		return
	}
	h.server = nil
	var wg sync.WaitGroup
	for _, handler := range h.handlers {
		if handler.getPerspective() == protocol.PerspectiveServer {
			wg.Add(1)
			go func(handler packetHandler) {
				// blocks until the CONNECTION_CLOSE has been sent and the run-loop has stopped
				handler.shutdown()
				wg.Done()
			}(handler)
		}
	}
	h.mutex.Unlock()
	wg.Wait()
}

func (h *packetHandlerMap) listen() {
	defer close(h.listening)
	for {
		p, err := h.conn.ReadPacket()
		if nerr, ok := err.(net.Error); ok && nerr.Temporary() {
			h.logger.Debugf("Temporary error reading from conn: %w", err)
			continue
		}
		if err != nil {
			h.close(err)
			return
		}
		h.handlePacket(p)
	}
}

func (h *packetHandlerMap) logUsage() {

}

func (h *packetHandlerMap) close(err error) {

}

func (h *packetHandlerMap) handlePacket(p *receivedPacket) {
	
}

var _ packetHandlerManager = &packetHandlerMap{}

// only print warnings about the UPD receive buffer size once
var receiveBufferWarningOnce sync.Once

func newPacketHandlerMap(c net.PacketConn,
	connIDLen int,
	statelessResetKey []byte,
	tracer logging.Tracer,
	logger utils.Logger) (packetHandlerManager, error) {
	if err := setReceiveBuffer(c, logger); err != nil {
		receiveBufferWarningOnce.Do(func() {
			log.Printf("%s. See https://github.com/lucas-clemente/quic-go/wiki/UDP-Receive-Buffer-Size for details.", err)
		})
	}
	conn, err := wrapConn(c)
	if err != nil {
		return nil, err
	}
	m := &packetHandlerMap{
		conn:                       conn,
		connIDLen:                  connIDLen,
		listening:                  make(chan struct{}),
		handlers:                   make(map[string]packetHandler),
		resetTokens:                make(map[protocol.StatelessResetToken]packetHandler),
		deleteRetiredSessionsAfter: protocol.RetiredConnectionIDDeleteTimeout,
		statelessResetEnabled:      len(statelessResetKey) > 0,
		statelessResetHasher:       hmac.New(sha256.New, statelessResetKey),
		tracer:                     tracer,
		logger:                     logger,
	}
	go m.listen()
	if logger.Debug() {
		go m.logUsage()
	}
	return m, nil
}

func setReceiveBuffer(c net.PacketConn, logger utils.Logger) error {
	conn, ok := c.(interface{ SetReadBuffer(int) error })
	if !ok {
		return errors.New("connection doesn't allow setting of receive buffer")
	}
	size, err := inspectReadBuffer(c)
	if err != nil {
		return fmt.Errorf("failed to determine receive buffer size: %w", err)
	}
	if size > protocol.DesiredReceiveBufferSize {
		logger.Debugf("Conn has receive buffer of %d kiB (wanted: at least %d kiB)", size/1024, protocol.DesiredReceiveBufferSize/1024)
	}
	if err := conn.SetReadBuffer(protocol.DesiredReceiveBufferSize); err != nil {
		return fmt.Errorf("failed to increase receive buffer size: %w", err)
	}
	newSize, err := inspectReadBuffer(c)
	if err != nil {
		return fmt.Errorf("failed to determine receive buffer size: %w", err)
	}
	if newSize == size {
		return fmt.Errorf("failed to determine receive buffer size: %w", err)
	}
	if newSize < protocol.DesiredReceiveBufferSize {
		return fmt.Errorf("failed to sufficiently increase receive buffer size (was: %d kiB, wanted: %d kiB, got: %d kiB)", size/1024, protocol.DesiredReceiveBufferSize/1024, newSize/1024)
	}
	logger.Debugf("Increased receive buffer size to %d kiB", newSize/1024)
	return nil
}