package proxy

import "wangzhiqiang/s5proxy/conf"

type IProxy interface {
	WithConfig(cfg *conf.Config)
	Start() error
	Stop()
}

var (
	proxies = map[string]func() IProxy{
		"s5": func() IProxy {
			return &S5Proxy{}
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
