package proxy

import (
	"wangzhiqiang/s5proxy/config"
	"wangzhiqiang/s5proxy/proxy/s5"
)

type IProxy interface {
	WithConfig(cfg *config.Config)
	Start() error
	Stop()
}

var (
	proxies = map[string]func() IProxy{
		"s5": func() IProxy {
			return &s5.Proxy{}
		},
	}
)

func Get(cfg *config.Config) []IProxy {
	var ps []IProxy
	for _, f := range proxies {
		f2 := f()
		f2.WithConfig(cfg)
		ps = append(ps, f2)
	}
	return ps
}
