package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// sink receives the frames of one stream channel. Both methods run on the
// reader goroutine and must never block; deliver also runs with the sink
// table locked, so it must not call register/closeChannel.
type sink interface {
	deliver(f frame)
	lost(err error)
}

type discardSink struct{}

func (discardSink) deliver(frame) {}
func (discardSink) lost(error)    {}

type rpcReply struct {
	result json.RawMessage
	err    error
}

// maxEarlyFrames bounds frames buffered for channels nobody registered yet.
const maxEarlyFrames = 1024

// Client is a connection to a herdr-core. It is safe for concurrent use.
type Client struct {
	conn    io.ReadWriteCloser
	br      *bufio.Reader
	hello   Hello
	timeout time.Duration

	// Writer queue: frames are written by one goroutine so they never interleave.
	wmu    sync.Mutex
	wq     [][]byte
	wsig   chan struct{}
	wclose bool

	mu      sync.Mutex // pending, nextID, err
	nextID  uint64
	pending map[uint64]chan rpcReply
	err     error
	done    chan struct{}

	smu   sync.Mutex // sinks, early
	sinks map[uint32]sink
	early map[uint32][]frame
	dead  bool

	wg sync.WaitGroup
}

// Connect dials the core (or, with AutoStart, runs "<binary> connect"), performs
// the framed hello handshake and verifies compatibility with Check.
func Connect(ctx context.Context, o Options) (*Client, error) {
	conn, err := dialCore(ctx, o)
	if err != nil {
		if !o.AutoStart {
			return nil, fmt.Errorf("herdr-core: %w", err)
		}
		pc, perr := startPump(o)
		if perr != nil {
			return nil, fmt.Errorf("herdr-core: %v; auto-start failed: %w", err, perr)
		}
		return newClient(ctx, pc, o.CallTimeout)
	}
	return newClient(ctx, conn, o.CallTimeout)
}

// newClient runs the handshake on conn and starts the I/O goroutines. It
// closes conn on failure.
func newClient(ctx context.Context, conn io.ReadWriteCloser, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		timeout = DefaultCallTimeout
	}
	c := &Client{
		conn:    conn,
		br:      bufio.NewReaderSize(conn, 64<<10),
		timeout: timeout,
		wsig:    make(chan struct{}, 1),
		pending: map[uint64]chan rpcReply{},
		done:    make(chan struct{}),
		sinks:   map[uint32]sink{},
		early:   map[uint32][]frame{},
	}
	hctx, cancel := context.WithTimeout(ctx, timeout)
	h, err := c.handshake(hctx)
	cancel()
	if err != nil {
		conn.Close()
		return nil, err
	}
	c.hello = h
	if err := Check(h); err != nil {
		conn.Close()
		return nil, err
	}
	c.wg.Add(2)
	go c.readLoop()
	go c.writeLoop()
	return c, nil
}

const helloLine = `{"jsonrpc":"2.0","id":0,"method":"hello","params":{"framed":true}}` + "\n"

func (c *Client) handshake(ctx context.Context) (Hello, error) {
	stop, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case <-ctx.Done():
			c.conn.Close()
		case <-stop:
		}
	}()
	defer func() { close(stop); <-exited }()

	fail := func(err error) (Hello, error) {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return Hello{}, fmt.Errorf("herdr-core: hello: %w", err)
	}
	if _, err := io.WriteString(c.conn, helloLine); err != nil {
		return fail(err)
	}
	line, err := c.br.ReadBytes('\n')
	if err != nil {
		return fail(err)
	}
	var m struct {
		Result Hello     `json:"result"`
		Error  *rpcError `json:"error"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return fail(fmt.Errorf("bad reply: %w", err))
	}
	if m.Error != nil {
		return Hello{}, m.Error.toError()
	}
	if !m.Result.Framed {
		return Hello{}, errors.New("herdr-core: core did not enable framed mode")
	}
	return m.Result, nil
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e *rpcError) toError() *RPCError {
	out := &RPCError{Code: e.Code, Message: e.Message}
	var d struct {
		Kind string `json:"kind"`
		Hint string `json:"hint"`
	}
	if len(e.Data) > 0 && json.Unmarshal(e.Data, &d) == nil {
		out.Kind, out.Hint = d.Kind, d.Hint
	}
	return out
}

// Hello returns the core's hello reply.
func (c *Client) Hello() Hello { return c.hello }

// Done is closed when the connection is lost or the client is closed.
func (c *Client) Done() <-chan struct{} { return c.done }

// Close closes the connection and waits for the client goroutines.
func (c *Client) Close() error {
	c.fail(ErrClosed)
	c.wg.Wait()
	return nil
}

// errNow returns the error describing why the client is unusable.
func (c *Client) errNow() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		return nil
	}
	if errors.Is(c.err, ErrClosed) {
		return ErrClosed
	}
	return fmt.Errorf("%w: %v", ErrLost, c.err)
}

// fail tears the connection down once.
func (c *Client) fail(cause error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = cause
	close(c.done)
	c.mu.Unlock()

	c.wmu.Lock()
	c.wclose = true
	c.wq = nil
	c.wmu.Unlock()

	c.conn.Close()

	c.smu.Lock()
	sinks := c.sinks
	c.sinks, c.early, c.dead = nil, nil, true
	c.smu.Unlock()
	for _, s := range sinks {
		s.lost(ErrLost)
	}
}

// send queues one frame for the writer; it never blocks.
func (c *Client) send(channel uint32, kind byte, payload []byte) error {
	if len(payload) > maxFrameSize {
		return errFrameTooLarge
	}
	b := encodeFrame(channel, kind, payload)
	c.wmu.Lock()
	if c.wclose {
		c.wmu.Unlock()
		if err := c.errNow(); err != nil {
			return err
		}
		return ErrLost
	}
	c.wq = append(c.wq, b)
	c.wmu.Unlock()
	select {
	case c.wsig <- struct{}{}:
	default:
	}
	return nil
}

func (c *Client) writeLoop() {
	defer c.wg.Done()
	var buf []byte
	for {
		select {
		case <-c.wsig:
		case <-c.done:
			return
		}
		c.wmu.Lock()
		q := c.wq
		c.wq = nil
		c.wmu.Unlock()
		buf = buf[:0]
		for _, b := range q {
			buf = append(buf, b...)
		}
		if len(buf) == 0 {
			continue
		}
		if _, err := c.conn.Write(buf); err != nil {
			c.fail(err)
			return
		}
		if cap(buf) > 1<<20 {
			buf = nil
		}
	}
}

func (c *Client) readLoop() {
	defer c.wg.Done()
	for {
		f, err := readFrame(c.br)
		if err != nil {
			c.fail(err)
			return
		}
		if f.channel == 0 {
			if f.kind == kindData {
				c.handleReply(f.payload)
			}
			continue
		}
		c.dispatch(f)
	}
}

func (c *Client) handleReply(payload []byte) {
	var m struct {
		ID     *uint64         `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if json.Unmarshal(payload, &m) != nil || m.ID == nil {
		return
	}
	c.mu.Lock()
	ch := c.pending[*m.ID]
	delete(c.pending, *m.ID)
	c.mu.Unlock()
	if ch == nil {
		return
	}
	r := rpcReply{result: m.Result}
	if m.Error != nil {
		r.err = m.Error.toError()
	}
	ch <- r
}

func (c *Client) dispatch(f frame) {
	c.smu.Lock()
	defer c.smu.Unlock()
	if c.dead {
		return
	}
	s := c.sinks[f.channel]
	if s == nil {
		if len(c.early[f.channel]) < maxEarlyFrames {
			c.early[f.channel] = append(c.early[f.channel], f)
		}
		return
	}
	s.deliver(f)
	if f.kind == kindClose {
		delete(c.sinks, f.channel)
	}
}

// register routes a channel's frames to s, first replaying frames that
// arrived before the registration. If the client is already dead, s is told.
func (c *Client) register(channel uint32, s sink) {
	c.smu.Lock()
	if c.dead {
		c.smu.Unlock()
		s.lost(ErrLost)
		return
	}
	c.sinks[channel] = s
	early := c.early[channel]
	delete(c.early, channel)
	// Replay under the lock so live frames cannot overtake the buffered ones.
	for _, f := range early {
		s.deliver(f)
		if f.kind == kindClose {
			delete(c.sinks, channel)
			break
		}
	}
	c.smu.Unlock()
}

// closeChannel detaches from a channel: later frames are ignored until the
// core acknowledges with its own CLOSE.
func (c *Client) closeChannel(channel uint32) {
	c.smu.Lock()
	if !c.dead {
		c.sinks[channel] = discardSink{}
		delete(c.early, channel)
	}
	c.smu.Unlock()
	_ = c.send(channel, kindClose, nil)
}

// Call invokes an RPC method. params is marshaled as the by-name params
// object (nil = {}); the reply's result is decoded into result (if non-nil).
// Error replies are returned as *RPCError.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	if params == nil {
		params = struct{}{}
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	id := atomic.AddUint64(&c.nextID, 1)
	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      uint64 `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", id, method, params})
	if err != nil {
		return fmt.Errorf("herdr-core: %s: %w", method, err)
	}
	ch := make(chan rpcReply, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.errNow()
	}
	c.pending[id] = ch
	c.mu.Unlock()
	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}
	if err := c.send(0, kindData, body); err != nil {
		forget()
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if result == nil || len(r.result) == 0 {
			return nil
		}
		if err := json.Unmarshal(r.result, result); err != nil {
			return fmt.Errorf("herdr-core: %s: bad result: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		forget()
		return fmt.Errorf("herdr-core: %s: %w", method, ctx.Err())
	case <-c.done:
		forget()
		return c.errNow()
	}
}

// pumpConn is a core connection through a "<binary> connect" child process.
type pumpConn struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   io.ReadCloser
	once  sync.Once
}

func (p *pumpConn) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *pumpConn) Write(b []byte) (int, error) { return p.stdin.Write(b) }

func (p *pumpConn) Close() error {
	p.once.Do(func() {
		p.stdin.Close()
		_ = p.cmd.Process.Kill()
		p.out.Close()
		_ = p.cmd.Wait()
	})
	return nil
}

// startPump runs "<binary> connect", which auto-starts the core and relays
// bytes between its stdio and the core socket.
func startPump(o Options) (*pumpConn, error) {
	bin, err := FindBinary(o.Binary)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, "connect")
	cmd.Env = os.Environ()
	if o.Session != "" {
		cmd.Env = append(cmd.Env, "HC_SESSION="+o.Session)
	}
	if o.Socket != "" {
		cmd.Env = append(cmd.Env, "HC_SOCKET="+o.Socket)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s connect: %w", bin, err)
	}
	return &pumpConn{cmd: cmd, stdin: in, out: out}, nil
}
