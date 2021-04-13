package hc

type HCRelayConfig struct {
	Trigger string `yaml:"trigger"`
	Secure  bool   `yaml:"secure"`
	Url     string `yaml:"url"`
	Channel string `yaml:"channel"`
	Retries struct {
		Force      bool   `yaml:"force"`
		MaxRetries int    `yaml:"max"`
		Delay      string `yaml:"delay"`
	} `yaml:"retries"`
	Password string `yaml:"password"`
}
