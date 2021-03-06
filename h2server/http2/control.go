package http2

import (
	"golang.org/x/net/http2"
	"sync"
	"time"
)

const (
	//默认的滑动窗口大小
	defaultWindowSize = 65535
	//初始化滑动窗口大小
	initialWindowSize             = defaultWindowSize
	initialConnWindowSize         = defaultWindowSize * 16
	defaultServerKeepaliveTime    = 5 * time.Minute
	defaultServerKeepaliveTimeout = 1 * time.Minute
)

type transportInFlow struct {
	limit   uint32
	unacked uint32
}

type quotaPool struct {
	c     chan int
	mu    sync.Mutex
	quota int
}

func newQuotaPool(q int) *quotaPool {
	qb := &quotaPool{
		c: make(chan int, 1),
	}
	if q > 0 {
		qb.c <- q
	} else {
		qb.quota = q
	}
	return qb
}

func (qp *quotaPool) add(v int) {
	qp.mu.Lock()
	defer qp.mu.Unlock()
	select {
	case n := <-qp.c:
		qp.quota += n
	default:
	}
	qp.quota += v
	if qp.quota <= 0 {
		return
	}
	select {
	case qp.c <- qp.quota:
		qp.quota = 0
	default:
	}
}

func (qp *quotaPool) acquire() <-chan int {
	return qp.c
}

type windowUpdate struct {
	streamId  uint32
	increment uint32
}

func (*windowUpdate) item() {

}

type settings struct {
	ack bool
	ss  []http2.Setting
}

func (*settings) item() {

}

type resetStream struct {
	streamId uint32
	code     http2.ErrCode
}

func (rs *resetStream) item() {

}

type goAway struct {
	code      http2.ErrCode
	debugData []byte
	headsUp   bool
	closeCoon bool
}

func (*goAway) item() {

}

type flushIO struct {
}

func (*flushIO) item() {

}

type ping struct {
	ack  bool
	data [8]byte
}

func (*ping) item() {

}

type inFlow struct {
	//limit pending data
	limit uint32
	mu    sync.Mutex
	//被接收但没有被消费
	pendingData uint32
	//被消费但没有发送给对端
	pendingUpdate uint32
}
