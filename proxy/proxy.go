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

func Get() []IProxy {
	var ps []IProxy
	for _, f := range proxies {
		f2 := f()
		ps = append(ps, f2)
	}
	return ps
}
