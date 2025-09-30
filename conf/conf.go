package conf

type Config struct {
	Listen       string            `mapstructure:"listen"`
	MaxConns     int               `mapstructure:"max_conns"`
	Users        map[string]string `mapstructure:"users"`
	AllowedHosts []string          `mapstructure:"allowed_hosts"`
}
