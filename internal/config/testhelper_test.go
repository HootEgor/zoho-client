package config

import "github.com/ilyakaznacheev/cleanenv"

// readForTest decodes a config file without going through MustLoad's sync.Once, so several
// fixtures can be loaded in one test binary.
func readForTest(path string, c *Config) error {
	return cleanenv.ReadConfig(path, c)
}
