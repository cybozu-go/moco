// +build !go1.8

package well

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cybozu-go/log"
	"github.com/cybozu-go/netutil"
)

const (
	defaultHTTPReadTimeout = 30 * time.Second

	// request tracking header.
	defaultRequestIDHeader = "X-Cybozu-Request-ID"

	requestIDHeaderEnv = "REQUEST_ID_HEADER"
)

var (
	requestIDHeader = defaultRequestIDHeader
)

func init() {
	hn := os.Getenv(requestIDHeaderEnv)
	if len(hn) > 0 {
		requestIDHeader = hn
	}
}

// HTTPServer is a wrapper for http.Server.
//
// This struct overrides Serve and ListenAndServe* methods.
//
// http.Server members are replaced as following:
//    - Handler is replaced with a wrapper handler.
//    - ReadTimeout is set to 30 seconds if it is zero.
//    - ConnState is replaced with the one provided by the framework.
type HTTPServer struct {
	*http.Server

	// AccessLog is a logger for access logs.
	// If this is nil, the default logger is used.
	AccessLog *log.Logger

	// ShutdownTimeout is the maximum duration the server waits for
	// all connections to be closed before shutdown.
	//
	// Zero duration disables timeout.
	ShutdownTimeout time.Duration

	// Env is the environment where this server runs.
	//
	// The global environment is used if Env is nil.
	Env *Environment

	handler  http.Handler
	wg       sync.WaitGroup
	timedout int32

	mu        sync.Mutex
	idleConns map[net.Conn]struct{}
	generator *IDGenerator

	initOnce sync.Once
}

// StdResponseWriter is the interface implemented by
// the ResponseWriter from http.Server.
//
// HTTPServer's ResponseWriter implements this as well.
type StdResponseWriter interface {
	http.ResponseWriter
	io.ReaderFrom
	http.Flusher
	http.CloseNotifier
	http.Hijacker
	WriteString(data string) (int, error)
}

type logResponseWriter struct {
	StdResponseWriter
	status int
	size   int64
}

func (w *logResponseWriter) WriteHeader(status int) {
	w.status = status
	w.StdResponseWriter.WriteHeader(status)
}

func (w *logResponseWriter) Write(data []byte) (int, error) {
	n, err := w.StdResponseWriter.Write(data)
	w.size += int64(n)
	return n, err
}

func (w *logResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	n, err := w.StdResponseWriter.ReadFrom(r)
	w.size += n
	return n, err
}

func (w *logResponseWriter) WriteString(data string) (int, error) {
	n, err := w.StdResponseWriter.WriteString(data)
	w.size += int64(n)
	return n, err
}

// ServeHTTP implements http.Handler interface.
func (s *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	lw := &logResponseWriter{w.(StdResponseWriter), http.StatusOK, 0}
	ctx, cancel := context.WithCancel(s.Env.ctx)
	defer cancel()

	reqid := r.Header.Get(requestIDHeader)
	if len(reqid) == 0 {
		reqid = s.generator.Generate()
	}
	ctx = WithRequestID(ctx, reqid)

	s.handler.ServeHTTP(lw, r.WithContext(ctx))

	fields := map[string]interface{}{
		log.FnType:           "access",
		log.FnResponseTime:   time.Since(startTime).Seconds(),
		log.FnProtocol:       r.Proto,
		log.FnHTTPStatusCode: lw.status,
		log.FnHTTPMethod:     r.Method,
		log.FnURL:            r.RequestURI,
		log.FnHTTPHost:       r.Host,
		log.FnRequestSize:    r.ContentLength,
		log.FnResponseSize:   lw.size,
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		fields[log.FnRemoteAddress] = ip
	}
	ua := r.Header.Get("User-Agent")
	if len(ua) > 0 {
		fields[log.FnHTTPUserAgent] = ua
	}
	if len(reqid) > 0 {
		fields[log.FnRequestID] = reqid
	}

	lv := log.LvInfo
	switch {
	case 500 <= lw.status:
		lv = log.LvError
	case 400 <= lw.status:
		lv = log.LvWarn
	}
	s.AccessLog.Log(lv, "well: access", fields)
}

func (s *HTTPServer) init() {
	if s.handler != nil {
		return
	}

	s.idleConns = make(map[net.Conn]struct{}, 100000)
	s.generator = NewIDGenerator()

	if s.Server.Handler == nil {
		panic("Handler must not be nil")
	}
	s.handler = s.Server.Handler
	s.Server.Handler = s
	if s.Server.ReadTimeout == 0 {
		s.Server.ReadTimeout = defaultHTTPReadTimeout
	}
	s.Server.ConnState = func(c net.Conn, state http.ConnState) {
		s.mu.Lock()
		if state == http.StateIdle {
			s.idleConns[c] = struct{}{}
		} else {
			delete(s.idleConns, c)
		}
		s.mu.Unlock()

		if state == http.StateNew {
			s.wg.Add(1)
			return
		}
		if state == http.StateHijacked || state == http.StateClosed {
			s.wg.Done()
		}
	}

	if s.AccessLog == nil {
		s.AccessLog = log.DefaultLogger()
	}

	if s.Env == nil {
		s.Env = defaultEnv
	}
	s.Env.Go(s.wait)
}

func (s *HTTPServer) wait(ctx context.Context) error {
	<-ctx.Done()

	s.Server.SetKeepAlivesEnabled(false)

	ch := make(chan struct{})

	// Interrupt conn.Read for idle connections.
	//
	// This must be run inside for-loop to catch connections
	// going idle at critical timing to acquire s.mu
	go func() {
	AGAIN:
		s.mu.Lock()
		for conn := range s.idleConns {
			conn.SetReadDeadline(time.Now())
		}
		s.mu.Unlock()
		select {
		case <-ch:
			return
		default:
		}
		time.Sleep(10 * time.Millisecond)
		goto AGAIN
	}()

	go func() {
		s.wg.Wait()
		close(ch)
	}()

	if s.ShutdownTimeout == 0 {
		<-ch
		return nil
	}

	select {
	case <-ch:
	case <-time.After(s.ShutdownTimeout):
		log.Warn("well: timeout waiting for shutdown", nil)
		atomic.StoreInt32(&s.timedout, 1)
	}
	return nil
}

// TimedOut returns true if the server shut down before all connections
// got closed.
func (s *HTTPServer) TimedOut() bool {
	return atomic.LoadInt32(&s.timedout) != 0
}

// Serve overrides http.Server's Serve method.
//
// Unlike the original, this method returns immediately just after
// starting a goroutine to accept connections.
//
// The framework automatically closes l when the environment's Cancel
// is called.
//
// Serve always returns nil.
func (s *HTTPServer) Serve(l net.Listener) error {
	s.initOnce.Do(s.init)

	l = netutil.KeepAliveListener(l)

	go func() {
		<-s.Env.ctx.Done()
		l.Close()
	}()

	go func() {
		s.Server.Serve(l)
	}()

	return nil
}

// ListenAndServe overrides http.Server's method.
//
// Unlike the original, this method returns immediately just after
// starting a goroutine to accept connections.  To stop listening,
// call the environment's Cancel.
//
// ListenAndServe returns non-nil error if and only if net.Listen failed.
func (s *HTTPServer) ListenAndServe() error {
	addr := s.Server.Addr
	if addr == "" {
		addr = ":http"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// ListenAndServeTLS overrides http.Server's method.
//
// Unlike the original, this method returns immediately just after
// starting a goroutine to accept connections.  To stop listening,
// call the environment's Cancel.
//
// Another difference from the original is that certFile and keyFile
// must be specified.  If not, configure http.Server.TLSConfig
// manually and use Serve().
//
// HTTP/2 is always enabled.
//
// ListenAndServeTLS returns non-nil error if net.Listen failed
// or failed to load certificate files.
func (s *HTTPServer) ListenAndServeTLS(certFile, keyFile string) error {
	addr := s.Server.Addr
	if addr == "" {
		addr = ":https"
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}

	config := &tls.Config{
		NextProtos:               []string{"h2", "http/1.1"},
		Certificates:             []tls.Certificate{cert},
		PreferServerCipherSuites: true,
		ClientSessionCache:       tls.NewLRUClientSessionCache(0),
	}
	s.Server.TLSConfig = config

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	tlsListener := tls.NewListener(ln, config)
	return s.Serve(tlsListener)
}

// HTTPClient is a thin wrapper for *http.Client.
//
// This overrides Do method to add the request tracking header if
// the passed request's context brings a request tracking ID.  Do
// also records the request log to Logger.
//
// Do not use Get/Head/Post/PostForm.  They panics.
type HTTPClient struct {
	*http.Client

	// Severity is used to log successful requests.
	//
	// Zero suppresses logging.  Valid values are one of
	// log.LvDebug, log.LvInfo, and so on.
	//
	// Errors are always logged with log.LvError.
	Severity int

	// Logger for HTTP request.  If nil, the default logger is used.
	Logger *log.Logger
}

// Do overrides http.Client.Do.
//
// req's context should have been set by http.Request.WithContext
// for request tracking and context-based cancelation.
func (c *HTTPClient) Do(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	v := ctx.Value(RequestIDContextKey)
	if v != nil {
		req.Header.Set(requestIDHeader, v.(string))
	}
	st := time.Now()
	resp, err := c.Client.Do(req)

	logger := c.Logger
	if logger == nil {
		logger = log.DefaultLogger()
	}

	if err == nil && (c.Severity == 0 || !logger.Enabled(c.Severity)) {
		// successful logs are suppressed if c.Severity is 0 or
		// logger threshold is under c.Severity.
		return resp, err
	}

	fields := FieldsFromContext(ctx)
	fields[log.FnType] = "http"
	fields[log.FnResponseTime] = time.Since(st).Seconds()
	fields[log.FnHTTPMethod] = req.Method
	fields[log.FnURL] = req.URL.String()
	fields[log.FnStartAt] = st

	if err != nil {
		fields["error"] = err.Error()
		logger.Error("well: http", fields)
		return resp, err
	}

	fields[log.FnHTTPStatusCode] = resp.StatusCode
	logger.Log(c.Severity, "well: http", fields)
	return resp, err
}

// Get panics.
func (c *HTTPClient) Get(url string) (*http.Response, error) {
	panic("Use Do")
}

// Head panics.
func (c *HTTPClient) Head(url string) (*http.Response, error) {
	panic("Use Do")
}

// Post panics.
func (c *HTTPClient) Post(url, bodyType string, body io.Reader) (*http.Response, error) {
	panic("Use Do")
}

// PostForm panics.
func (c *HTTPClient) PostForm(url string, data url.Values) (*http.Response, error) {
	panic("Use Do")
}
