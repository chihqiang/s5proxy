package s5

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
	"wangzhiqiang/s5proxy/config"
)

var (
	TotalUpload   int64
	TotalDownload int64
)

type Proxy struct {
	listenAddr string
	maxConns   int
	users      map[string]string
	whitelist  []string

	listener    net.Listener
	connLimiter chan struct{}
	stopCh      chan struct{}
	wg          sync.WaitGroup
	mu          sync.Mutex
	closed      bool
}

// New 创建一个代理服务
func New(listen string, maxConns int, users map[string]string, whitelist []string) *Proxy {
	if maxConns <= 0 {
		maxConns = 1000
	}
	return &Proxy{
		listenAddr:  listen,
		maxConns:    maxConns,
		users:       users,
		whitelist:   whitelist,
		connLimiter: make(chan struct{}, maxConns),
		stopCh:      make(chan struct{}),
	}
}

func (p *Proxy) WithConfig(cfg *config.Config) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 1000
	}
	p.listenAddr = cfg.Listen
	p.users = cfg.Users
	p.whitelist = cfg.AllowedHosts
	p.maxConns = cfg.MaxConns
	p.connLimiter = make(chan struct{}, cfg.MaxConns)
	p.stopCh = make(chan struct{})
}

// Start 启动 socks5 代理服务
func (p *Proxy) Start() error {
	ln, err := net.Listen("tcp", p.listenAddr)
	if err != nil {
		return fmt.Errorf("listen failed: %w", err)
	}
	p.listener = ln
	slog.Info("SOCKS5 Proxy started", "listen", p.listenAddr, "max_conns", p.maxConns)

	go p.acceptLoop()
	return nil
}

// Stop 优雅关闭代理服务
func (p *Proxy) Stop() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()

	close(p.stopCh)
	if p.listener != nil {
		_ = p.listener.Close()
	}
	p.wg.Wait()
	slog.Info("SOCKS5 Proxy closed")
}

func (p *Proxy) acceptLoop() {
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			select {
			case <-p.stopCh:
				return
			default:
				slog.Error("Accept error", "error", err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}

		select {
		case p.connLimiter <- struct{}{}:
			p.wg.Add(1)
			go func(c net.Conn) {
				defer p.wg.Done()
				defer func() { <-p.connLimiter }()
				p.handleConnection(c)
			}(conn)
		default:
			slog.Warn("Too many connections, rejecting", "remote", conn.RemoteAddr().String())
			_ = conn.Close()
		}
	}
}

func (p *Proxy) handleConnection(client net.Conn) {
	defer client.Close()
	addr := client.RemoteAddr().String()
	slog.Info("New connection", "client", addr)

	buf := make([]byte, 262)
	if _, err := io.ReadFull(client, buf[:2]); err != nil {
		return
	}
	nMethods := int(buf[1])
	if _, err := io.ReadFull(client, buf[2:2+nMethods]); err != nil {
		return
	}

	// 仅支持用户名密码认证 0x02
	authSupported := false
	for i := 0; i < nMethods; i++ {
		if buf[2+i] == 0x02 {
			authSupported = true
			break
		}
	}
	if !authSupported {
		client.Write([]byte{0x05, 0xFF})
		slog.Warn("Client does not support username/password auth", "client", addr)
		return
	}
	client.Write([]byte{0x05, 0x02})

	// 用户名密码认证
	user, ok := p.doAuth(client)
	if !ok {
		slog.Warn("Auth failed", "client", addr)
		return
	}
	slog.Info("Auth success", "user", user, "client", addr)

	// 请求头
	if _, err := io.ReadFull(client, buf[:4]); err != nil {
		return
	}
	cmd := buf[1]
	atyp := buf[3]

	if cmd != 0x01 { // 只支持 CONNECT
		client.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	var dstAddr string
	switch atyp {
	case 0x01: // IPv4
		if _, err := io.ReadFull(client, buf[:6]); err != nil {
			return
		}
		ip := net.IPv4(buf[0], buf[1], buf[2], buf[3])
		port := binary.BigEndian.Uint16(buf[4:6])
		dstAddr = net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port))
	case 0x03: // 域名
		if _, err := io.ReadFull(client, buf[:1]); err != nil {
			return
		}
		dlen := int(buf[0])
		if _, err := io.ReadFull(client, buf[:dlen+2]); err != nil {
			return
		}
		host := string(buf[:dlen])
		port := binary.BigEndian.Uint16(buf[dlen : dlen+2])
		dstAddr = net.JoinHostPort(host, fmt.Sprintf("%d", port))
	default:
		client.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	// 白名单校验目标地址
	if len(p.whitelist) > 0 && !p.isAllowed(dstAddr) {
		slog.Warn("Target not in whitelist", "dst", dstAddr)
		client.Write([]byte{0x05, 0x02, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	remote, err := net.Dial("tcp", dstAddr)
	if err != nil {
		client.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		slog.Error("Failed to connect to remote", "dst", dstAddr, "error", err)
		return
	}
	defer remote.Close()

	client.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	slog.Info("Connection established", "user", user, "dst", dstAddr)

	// 双向转发，带统计
	upCounter := &counterWriter{}
	downCounter := &counterWriter{}

	go io.Copy(io.MultiWriter(remote, upCounter), client)
	io.Copy(io.MultiWriter(client, downCounter), remote)

	TotalUpload += upCounter.n
	TotalDownload += downCounter.n

	slog.Info("Connection closed",
		slog.String("user", user),
		slog.String("client", addr),
		slog.String("dst", dstAddr),
		slog.String("upload", fmt.Sprintf("%.2f KB", float64(upCounter.n)/1024)),
		slog.String("download", fmt.Sprintf("%.2f KB", float64(downCounter.n)/1024)),
	)
}

func (p *Proxy) doAuth(conn net.Conn) (string, bool) {
	buf := make([]byte, 512)

	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return "", false
	}
	ulen := int(buf[1])
	if _, err := io.ReadFull(conn, buf[:ulen+1]); err != nil {
		return "", false
	}
	username := string(buf[:ulen])
	plen := int(buf[ulen])
	if _, err := io.ReadFull(conn, buf[:plen]); err != nil {
		return "", false
	}
	password := string(buf[:plen])

	if expect, ok := p.users[username]; ok && expect == password {
		conn.Write([]byte{0x01, 0x00})
		return username, true
	}
	conn.Write([]byte{0x01, 0x01})
	return "", false
}

func (p *Proxy) isAllowed(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}

	// 先检查是否直接匹配（IP or 域名）
	for _, allow := range p.whitelist {
		if strings.HasPrefix(allow, "*.") {
			// 通配符匹配：例如 *.example.org
			suffix := strings.TrimPrefix(allow, "*.")
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
		} else if allow == host {
			return true
		}
	}
	// 再解析白名单域名为 IP，匹配目标 host 是否是 IP
	for _, allow := range p.whitelist {
		if strings.Contains(allow, "*") {
			continue // 通配符已处理
		}
		ips, err := net.LookupHost(allow)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			if ip == host {
				return true
			}
		}
	}
	return false
}

type counterWriter struct {
	n int64
}

func (w *counterWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.n += int64(n)
	return n, nil
}
