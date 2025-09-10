package app

// 导入所需的包
import (
	"context"                      // 用于控制 goroutine 的生命周期
	"errors"                       // 用于创建和处理错误
	"github.com/fsnotify/fsnotify" // 用于文件系统事件通知
	"github.com/spf13/viper"       // 用于配置管理
	"log/slog"                     // 用于结构化日志记录
	"sync"                         // 用于同步原语
	"time"                         // 用于时间相关操作
	"wangzhiqiang/s5proxy/conf"
	"wangzhiqiang/s5proxy/proxy" // 用于代理服务接口
)

// App 结构体定义了应用程序的核心组件
type App struct {
	mu       sync.Mutex // 互斥锁，用于保护共享资源
	filename string     // 配置文件路径

	proxies []proxy.IProxy // 代理服务列表
}

// NewApp 创建一个新的应用程序实例
// 参数:
//   - filename: 配置文件路径
//
// 返回值: 应用程序实例指针
func NewApp(filename string) *App {
	// 创建并返回应用程序实例
	return &App{
		filename: filename, // 设置配置文件路径
	}
}

// Run 启动应用程序并监听配置文件变化
// 参数:
//   - ctx: 上下文，用于控制应用程序的生命周期
//
// 返回值: 运行过程中可能发生的错误
func (a *App) Run(ctx context.Context) error {
	// 设置配置文件路径
	viper.SetConfigFile(a.filename)
	// 首次加载配置
	if err := a.reload(); err != nil {
		return err
	}
	// 启动配置监听
	viper.WatchConfig()

	// 定义防抖变量，用于避免频繁的配置重载
	var (
		debounceTimer *time.Timer              // 防抖定时器
		debounceDelay = 500 * time.Millisecond // 防抖延迟时间
	)

	// 设置配置变化回调函数
	viper.OnConfigChange(func(e fsnotify.Event) {
		// 如果已有定时器，则停止它
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
		// 启动新的防抖定时器
		debounceTimer = time.AfterFunc(debounceDelay, func() {
			a.mu.Lock()         // 加锁保护共享资源
			defer a.mu.Unlock() // 函数退出时解锁
			// 记录配置文件变化日志
			slog.Info("Config file changed", "file", e.Name)
			// 关闭现有代理服务
			a.shutdown()
			// 重新加载配置并启动代理服务
			if err := a.reload(); err != nil {
				// 如果重载失败，记录错误日志
				slog.Error("Config reload failed", "error", err)
			}
		})
	})

	// 阻塞直到上下文完成（收到停止信号）
	<-ctx.Done()
	// 记录关闭代理服务日志
	slog.Info("Shutting down proxy")
	// 关闭代理服务
	a.shutdown()
	return nil
}

// reload 重新加载配置并启动代理服务
// 返回值: 重新加载过程中可能发生的错误
func (a *App) reload() error {
	// 创建配置对象
	cfg := &conf.Config{}
	// 读取配置文件
	if err := viper.ReadInConfig(); err != nil {
		// 如果读取失败，记录错误日志并返回错误
		slog.Error("Failed to read config file", "error", err)
		return err
	}
	// 解析 YAML 配置到配置对象
	if err := viper.Unmarshal(cfg); err != nil {
		// 如果解析失败，记录错误日志并返回错误
		slog.Error("Failed to parse YAML config", "error", err)
		return err
	}

	// 验证必需字段
	// 检查监听地址是否为空
	if cfg.Listen == "" {
		return errors.New("config 'listen' cannot be empty")
	}
	// 检查用户列表是否为空
	if len(cfg.Users) == 0 {
		return errors.New("config 'users' cannot be empty")
	}

	// 如果没有代理服务（第一次启动），则关闭现有代理
	if len(a.proxies) == 0 {
		a.shutdown()
	}

	// 创建等待组和互斥锁用于并发控制
	var wg sync.WaitGroup
	var mu sync.Mutex

	// 遍历所有代理服务
	for _, pxy := range proxy.Get() {
		wg.Add(1) // 增加等待组计数
		// 启动新的 goroutine 来配置和启动代理服务
		go func(pxy proxy.IProxy, cfg *conf.Config) {
			defer wg.Done() // 函数退出时减少等待组计数
			// 使用新配置更新代理服务
			pxy.WithConfig(cfg)
			// 启动代理服务
			if err := pxy.Start(); err != nil {
				// 如果启动失败，记录错误日志
				slog.Error("Failed to start proxy", "error", err)
			}
			// 加锁保护共享资源
			mu.Lock()
			// 将代理服务添加到代理列表中
			a.proxies = append(a.proxies, pxy)
			// 解锁
			mu.Unlock()
		}(pxy, cfg) // 传递代理服务和配置对象
	}
	// 等待所有代理服务启动完成
	wg.Wait()
	return nil
}

// shutdown 关闭所有代理服务
func (a *App) shutdown() {
	// 遍历所有代理服务并关闭它们
	for _, pxy := range a.proxies {
		pxy.Stop() // 调用代理服务的停止方法
	}
	// 清空代理服务列表
	a.proxies = nil
}
