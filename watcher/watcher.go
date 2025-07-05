package watcher

import (
	"log/slog"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// WatchConfig 监听配置文件变化
// path: 绝对路径或相对路径
// onChange: 配置变动时触发的回调函数（带防抖）
func WatchConfig(path string, onChange func()) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	configDir := filepath.Dir(absPath)
	configFile := filepath.Base(absPath)
	err = watcher.Add(configDir)
	if err != nil {
		return err
	}
	go func() {
		defer watcher.Close()
		timer := time.NewTimer(0)
		<-timer.C // 立即关闭，等首次修改才触发
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Base(event.Name) != configFile {
					continue
				}
				// 检测写入、创建、重命名等变动
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
					slog.Info("Configuration file changes detected", slog.String("file", event.Name))
					// 防抖：重置 timer，延迟执行回调
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(500 * time.Millisecond)

					go func() {
						<-timer.C
						onChange()
					}()
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Error("watch Error", slog.String("error", err.Error()))
			}
		}
	}()

	slog.Info("Start configuration monitoring", slog.String("path", absPath))
	return nil
}
