package proxy

// 导入所需的包
import (
	"encoding/binary" // 用于处理二进制数据
	"fmt"             // 用于格式化字符串
	"io"              // 用于处理 I/O 操作
	"log/slog"        // 用于结构化日志记录
	"net"             // 用于网络操作
	"strings"         // 用于字符串处理
	"sync"            // 用于同步原语
	"time"            // 用于时间相关操作
	"wangzhiqiang/s5proxy/conf"
)

// 全局变量，用于统计总上传和下载数据量
var (
	TotalUpload   int64 // 总上传数据量
	TotalDownload int64 // 总下载数据量
)

// S5Proxy 结构体定义了 SOCKS5 代理服务器的核心组件
type S5Proxy struct {
	listenAddr string            // 监听地址
	maxConns   int               // 最大连接数
	users      map[string]string // 用户认证信息
	whitelist  []string          // 白名单列表

	listener    net.Listener   // 监听器
	connLimiter chan struct{}  // 连接限制器
	stopCh      chan struct{}  // 停止信号通道
	wg          sync.WaitGroup // 等待组，用于等待所有连接处理完毕
	mu          sync.Mutex     // 互斥锁，用于保护共享资源
	closed      bool           // 服务器关闭状态标志
}

// New 创建一个代理服务实例
// 参数:
//   - listen: 监听地址
//   - maxConns: 最大连接数
//   - users: 用户认证信息
//   - whitelist: 白名单列表
//
// 返回值: 代理服务实例指针
func New(listen string, maxConns int, users map[string]string, whitelist []string) *S5Proxy {
	// 如果最大连接数小于等于0，则设置默认值1000
	if maxConns <= 0 {
		maxConns = 1000
	}
	// 创建并返回代理服务实例
	return &S5Proxy{
		listenAddr:  listen,                        // 设置监听地址
		maxConns:    maxConns,                      // 设置最大连接数
		users:       users,                         // 设置用户认证信息
		whitelist:   whitelist,                     // 设置白名单列表
		connLimiter: make(chan struct{}, maxConns), // 创建连接限制器
		stopCh:      make(chan struct{}),           // 创建停止信号通道
	}
}

// WithConfig 使用配置对象更新代理服务配置
// 参数:
//   - cfg: 配置对象指针
func (p *S5Proxy) WithConfig(cfg *conf.Config) {
	p.mu.Lock()         // 加锁保护共享资源
	defer p.mu.Unlock() // 函数退出时解锁

	// 如果配置中的最大连接数小于等于0，则设置默认值1000
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 1000
	}
	// 更新代理服务的各项配置
	p.listenAddr = cfg.Listen                         // 更新监听地址
	p.users = cfg.Users                               // 更新用户认证信息
	p.whitelist = cfg.AllowedHosts                    // 更新白名单列表
	p.maxConns = cfg.MaxConns                         // 更新最大连接数
	p.connLimiter = make(chan struct{}, cfg.MaxConns) // 重新创建连接限制器
	p.stopCh = make(chan struct{})                    // 重新创建停止信号通道
}

// Start 启动 SOCKS5 代理服务
// 返回值: 启动过程中可能发生的错误
func (p *S5Proxy) Start() error {
	// 创建 TCP 监听器
	ln, err := net.Listen("tcp", p.listenAddr)
	if err != nil {
		// 如果监听失败，返回包装后的错误
		return fmt.Errorf("listen failed: %w", err)
	}
	p.listener = ln // 保存监听器引用
	// 记录代理服务启动日志
	slog.Info("SOCKS5 S5Proxy started", "listen", p.listenAddr, "max_conns", p.maxConns)

	// 启动接受连接的循环（在单独的 goroutine 中）
	go p.acceptLoop()
	return nil // 启动成功，返回 nil
}

// Stop 优雅关闭代理服务
func (p *S5Proxy) Stop() {
	p.mu.Lock() // 加锁保护共享资源
	// 检查服务是否已经关闭
	if p.closed {
		p.mu.Unlock() // 解锁
		return        // 如果已关闭则直接返回
	}
	p.closed = true // 设置关闭状态标志
	p.mu.Unlock()   // 解锁

	// 关闭停止信号通道，通知所有相关 goroutine 停止工作
	close(p.stopCh)
	// 关闭监听器
	if p.listener != nil {
		_ = p.listener.Close()
	}
	// 等待所有连接处理完毕
	p.wg.Wait()

	// 等待一段时间确保端口完全释放
	time.Sleep(100 * time.Millisecond)
	// 记录代理服务关闭日志
	slog.Info("SOCKS5 S5Proxy closed")
}

// acceptLoop 接受并处理客户端连接的主循环
func (p *S5Proxy) acceptLoop() {
	// 无限循环，持续接受连接
	for {
		// 接受新的客户端连接
		conn, err := p.listener.Accept()
		if err != nil {
			// 如果接受连接时发生错误
			select {
			// 检查是否收到了停止信号
			case <-p.stopCh:
				return // 如果收到停止信号，则退出循环
			default:
				// 记录错误日志
				slog.Error("Accept error", "error", err)
				// 等待一段时间后继续尝试接受连接
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}

		// 尝试获取连接许可
		select {
		case p.connLimiter <- struct{}{}: // 如果有可用的连接许可
			p.wg.Add(1) // 增加等待组计数
			// 启动新的 goroutine 处理连接
			go func(c net.Conn) {
				defer p.wg.Done()                  // 函数退出时减少等待组计数
				defer func() { <-p.connLimiter }() // 函数退出时释放连接许可
				p.handleConnection(c)              // 处理连接
			}(conn)
		default:
			// 如果没有可用的连接许可（达到最大连接数）
			// 记录警告日志
			slog.Warn("Too many connections, rejecting", "remote", conn.RemoteAddr().String())
			// 关闭连接
			_ = conn.Close()
		}
	}
}

// handleConnection 处理单个客户端连接
// 参数:
//   - client: 客户端连接
func (p *S5Proxy) handleConnection(client net.Conn) {
	defer client.Close()                        // 函数退出时关闭客户端连接
	addr := client.RemoteAddr().String()        // 获取客户端地址
	slog.Info("New connection", "client", addr) // 记录新连接日志

	// 创建缓冲区用于读取 SOCKS5 协议数据
	buf := make([]byte, 262)
	// 读取 SOCKS5 协议版本和认证方法数量
	if _, err := io.ReadFull(client, buf[:2]); err != nil {
		return // 如果读取失败则返回
	}
	nMethods := int(buf[1]) // 获取认证方法数量
	// 读取所有支持的认证方法
	if _, err := io.ReadFull(client, buf[2:2+nMethods]); err != nil {
		return // 如果读取失败则返回
	}

	// 检查是否支持用户名密码认证 (0x02)
	authSupported := false
	for i := 0; i < nMethods; i++ {
		if buf[2+i] == 0x02 { // 如果支持用户名密码认证
			authSupported = true
			break
		}
	}
	// 如果不支持用户名密码认证
	if !authSupported {
		// 发送认证失败响应
		client.Write([]byte{0x05, 0xFF})
		// 记录警告日志
		slog.Warn("Client does not support username/password auth", "client", addr)
		return // 返回
	}
	// 发送选择用户名密码认证的响应
	client.Write([]byte{0x05, 0x02})

	// 进行用户名密码认证
	user, ok := p.doAuth(client)
	if !ok { // 如果认证失败
		slog.Warn("Auth failed", "client", addr) // 记录警告日志
		return                                   // 返回
	}
	// 记录认证成功日志
	slog.Info("Auth success", "user", user, "client", addr)

	// 读取 SOCKS5 请求头
	if _, err := io.ReadFull(client, buf[:4]); err != nil {
		return // 如果读取失败则返回
	}
	cmd := buf[1]  // 获取命令类型
	atyp := buf[3] // 获取地址类型

	// 检查是否为 CONNECT 命令 (0x01)
	if cmd != 0x01 {
		// 如果不是 CONNECT 命令，发送不支持的命令响应
		client.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return // 返回
	}

	// 根据地址类型解析目标地址
	var dstAddr string
	switch atyp {
	case 0x01: // IPv4 地址
		// 读取 IPv4 地址和端口
		if _, err := io.ReadFull(client, buf[:6]); err != nil {
			return // 如果读取失败则返回
		}
		ip := net.IPv4(buf[0], buf[1], buf[2], buf[3])                   // 解析 IP 地址
		port := binary.BigEndian.Uint16(buf[4:6])                        // 解析端口
		dstAddr = net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port)) // 构造目标地址
	case 0x03: // 域名
		// 读取域名长度
		if _, err := io.ReadFull(client, buf[:1]); err != nil {
			return // 如果读取失败则返回
		}
		dlen := int(buf[0]) // 获取域名长度
		// 读取域名和端口
		if _, err := io.ReadFull(client, buf[:dlen+2]); err != nil {
			return // 如果读取失败则返回
		}
		host := string(buf[:dlen])                                // 获取域名
		port := binary.BigEndian.Uint16(buf[dlen : dlen+2])       // 获取端口
		dstAddr = net.JoinHostPort(host, fmt.Sprintf("%d", port)) // 构造目标地址
	default:
		// 如果是不支持的地址类型，发送响应
		client.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return // 返回
	}

	// 检查目标地址是否在白名单中
	if len(p.whitelist) > 0 && !p.isAllowed(dstAddr) {
		// 如果不在白名单中，记录警告日志并发送响应
		slog.Warn("Target not in whitelist", "dst", dstAddr)
		client.Write([]byte{0x05, 0x02, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return // 返回
	}

	// 连接到目标服务器
	remote, err := net.Dial("tcp", dstAddr)
	if err != nil { // 如果连接失败
		// 发送连接失败响应
		client.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		// 记录错误日志
		slog.Error("Failed to connect to remote", "dst", dstAddr, "error", err)
		return // 返回
	}
	defer remote.Close() // 函数退出时关闭远程连接

	// 发送连接成功响应
	client.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	// 记录连接建立日志
	slog.Info("Connection established", "user", user, "dst", dstAddr)

	// 创建上传和下载计数器
	upCounter := &counterWriter{}
	downCounter := &counterWriter{}

	// 启动双向数据转发
	go io.Copy(io.MultiWriter(remote, upCounter), client) // 客户端到远程服务器的数据转发
	io.Copy(io.MultiWriter(client, downCounter), remote)  // 远程服务器到客户端的数据转发

	// 更新全局上传和下载统计数据
	TotalUpload += upCounter.n
	TotalDownload += downCounter.n

	// 记录连接关闭日志，包含传输统计数据
	slog.Info("Connection closed",
		slog.String("user", user),
		slog.String("client", addr),
		slog.String("dst", dstAddr),
		slog.String("upload", fmt.Sprintf("%.2f KB", float64(upCounter.n)/1024)),
		slog.String("download", fmt.Sprintf("%.2f KB", float64(downCounter.n)/1024)),
	)
}

// doAuth 执行用户名密码认证
// 参数:
//   - conn: 客户端连接
//
// 返回值:
//   - string: 认证成功的用户名
//   - bool: 认证是否成功
func (p *S5Proxy) doAuth(conn net.Conn) (string, bool) {
	// 创建缓冲区用于读取认证数据
	buf := make([]byte, 512)

	// 读取用户名长度
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return "", false // 如果读取失败，返回空用户名和失败状态
	}
	ulen := int(buf[1]) // 获取用户名长度
	// 读取用户名
	if _, err := io.ReadFull(conn, buf[:ulen+1]); err != nil {
		return "", false // 如果读取失败，返回空用户名和失败状态
	}
	username := string(buf[:ulen]) // 获取用户名
	plen := int(buf[ulen])         // 获取密码长度
	// 读取密码
	if _, err := io.ReadFull(conn, buf[:plen]); err != nil {
		return "", false // 如果读取失败，返回空用户名和失败状态
	}
	password := string(buf[:plen]) // 获取密码

	// 验证用户名和密码
	if expect, ok := p.users[username]; ok && expect == password {
		// 如果认证成功，发送成功响应
		conn.Write([]byte{0x01, 0x00})
		return username, true // 返回用户名和成功状态
	}
	// 如果认证失败，发送失败响应
	conn.Write([]byte{0x01, 0x01})
	return "", false // 返回空用户名和失败状态
}

// isAllowed 检查目标地址是否在白名单中
// 参数:
//   - addr: 目标地址
//
// 返回值: 是否允许连接到该地址
func (p *S5Proxy) isAllowed(addr string) bool {
	// 分解地址获取主机名
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false // 如果分解失败，返回 false
	}

	// 先检查是否直接匹配（IP 或域名）
	for _, allow := range p.whitelist {
		// 检查是否为通配符匹配（例如 *.example.org）
		if strings.HasPrefix(allow, "*.") {
			// 通配符匹配：例如 *.example.org
			suffix := strings.TrimPrefix(allow, "*.") // 获取后缀
			// 检查主机名是否以指定后缀结尾
			if strings.HasSuffix(host, "."+suffix) {
				return true // 如果匹配，返回 true
			}
		} else if allow == host {
			return true // 如果直接匹配，返回 true
		}
	}
	// 再解析白名单域名为 IP，检查目标主机是否匹配
	for _, allow := range p.whitelist {
		// 跳过通配符条目（已处理）
		if strings.Contains(allow, "*") {
			continue
		}
		// 解析白名单域名的 IP 地址
		ips, err := net.LookupHost(allow)
		if err != nil {
			continue // 如果解析失败，继续检查下一个
		}
		// 检查目标主机是否匹配解析出的 IP 地址
		for _, ip := range ips {
			if ip == host {
				return true // 如果匹配，返回 true
			}
		}
	}
	return false // 如果都不匹配，返回 false
}

// counterWriter 实现了 io.Writer 接口，用于统计写入的数据量
type counterWriter struct {
	n int64 // 写入的数据量计数
}

// Write 实现 io.Writer 接口
// 参数:
//   - p: 要写入的数据
//
// 返回值:
//   - int: 实际写入的字节数
//   - error: 写入过程中可能发生的错误
func (w *counterWriter) Write(p []byte) (int, error) {
	n := len(p)     // 获取数据长度
	w.n += int64(n) // 累加到计数器
	return n, nil   // 返回写入的字节数和 nil 错误
}
