package conf

type Config struct {
	Listen       string            `yaml:"listen"`
	MaxConns     int               `yaml:"max_conns"`
	Users        map[string]string `yaml:"users"`
	AllowedHosts []string          `yaml:"allowed_hosts"`
}
