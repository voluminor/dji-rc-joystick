package rc

import (
	"errors"
	"net"
	"strconv"
	"time"
)

// // // // // // // // // //

const (
	connectTimeout = 2 * time.Second
	readTimeout    = 20 * time.Millisecond
	drainTimeout   = 5 * time.Millisecond
	sendTimeout    = time.Second
	reconnectDelay = 100 * time.Millisecond
)

var errNotConnected = errors.New("rc: link is not connected")

// //

type linkObj struct {
	addr string
	conn net.Conn
}

func newLink(ip string, port int) *linkObj {
	return &linkObj{addr: net.JoinHostPort(ip, strconv.Itoa(port))}
}

func (obj *linkObj) connected() bool {
	return obj.conn != nil
}

func (obj *linkObj) connect() error {
	if obj.conn != nil {
		return nil
	}

	conn, err := net.DialTimeout("tcp", obj.addr, connectTimeout)
	if err != nil {
		return err
	}
	obj.conn = conn
	return nil
}

func (obj *linkObj) disconnect() {
	if obj.conn != nil {
		_ = obj.conn.Close()
		obj.conn = nil
	}
}

// read fills buf, returning (0, nil) on timeout. Any other failure closes
// the link and surfaces the error.
func (obj *linkObj) read(buf []byte, timeout time.Duration) (int, error) {
	if obj.conn == nil {
		return 0, errNotConnected
	}

	_ = obj.conn.SetReadDeadline(time.Now().Add(timeout))
	n, err := obj.conn.Read(buf)
	if n > 0 {
		return n, nil
	}
	if err == nil {
		return 0, nil
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return 0, nil
	}
	obj.disconnect()
	return 0, err
}

func (obj *linkObj) send(frame []byte) error {
	if obj.conn == nil {
		return errNotConnected
	}

	_ = obj.conn.SetWriteDeadline(time.Now().Add(sendTimeout))
	if _, err := obj.conn.Write(frame); err != nil {
		obj.disconnect()
		return err
	}
	return nil
}
