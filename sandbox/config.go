package sandbox

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const sandboxConfigEnv = "COWORK_SANDBOX_CONFIG"

func loadSandboxBaseConfig() (srtConfig, error) {
	path, err := sandboxConfigPath()
	if err != nil {
		return srtConfig{}, err
	}
	return loadSandboxBaseConfigAt(path)
}

func loadSandboxBaseConfigAt(path string) (srtConfig, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) == 0 {
			return normalizeSRTConfig(defaultSandboxBaseConfig()), nil
		}
		var config srtConfig
		if err := yaml.Unmarshal(data, &config); err != nil {
			return srtConfig{}, fmt.Errorf("sandbox: parsing %s: %w", path, err)
		}
		return normalizeSRTConfig(config), nil
	}
	if !os.IsNotExist(err) {
		return srtConfig{}, fmt.Errorf("sandbox: reading %s: %w", path, err)
	}

	config := normalizeSRTConfig(defaultSandboxBaseConfig())
	if err := writeDefaultSandboxConfig(path, config); err != nil {
		return srtConfig{}, fmt.Errorf("sandbox: creating default config %s: %w", path, err)
	}
	log.Printf("[sandbox] created default sandbox config %s", path)
	return config, nil
}

func sandboxConfigPath() (string, error) {
	if path := os.Getenv(sandboxConfigEnv); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("sandbox: finding user config dir: %w", err)
	}
	return filepath.Join(dir, "claude-cowork-service", "sandbox.yaml"), nil
}

func writeDefaultSandboxConfig(path string, config srtConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultSandboxConfigYAML(config)), 0644)
}

func defaultSandboxConfigYAML(config srtConfig) string {
	data, err := yaml.Marshal(config)
	if err != nil {
		return ""
	}
	return "# claude-cowork-service sandbox defaults\n" +
		"# Edit this file to extend or relax the baseline applied before per-spawn mounts and domains.\n" +
		"# /tmp and /var/tmp are denied as host reads so bubblewrap provides private writable tmpfs mounts there.\n" +
		"# Set network.allowAllUnixSockets: true to let sandboxed processes connect to host Unix sockets\n" +
		"# (Docker, ssh-agent, etc.). On Linux SRT cannot filter sockets by path, so this is all-or-nothing.\n" +
		string(data)
}

func defaultSandboxBaseConfig() srtConfig {
	return srtConfig{
		Network: networkConfig{
			AllowedDomains:      []string{},
			DeniedDomains:       []string{},
			AllowAllUnixSockets: false,
		},
		Filesystem: filesystemConfig{
			DenyRead:   defaultSandboxDenyRead(),
			AllowRead:  []string{"/var/lib"},
			AllowWrite: []string{},
			DenyWrite:  []string{},
		},
		Linux: linuxConfig{
			BindMounts: []bindMountConfig{},
		},
	}
}

func defaultSandboxDenyRead() []string {
	deny := []string{
		"/home",
		"/root",
		"/mnt",
		"/media",
		"/srv",
		"/tmp",
		"/var",
		"/var/tmp",
		filepath.ToSlash(filepath.Join("/run/user", fmt.Sprint(os.Getuid()))),
	}

	allowedRootEntries := map[string]struct{}{
		"bin":      {},
		"boot":     {},
		"dev":      {},
		"etc":      {},
		"home":     {},
		"lib":      {},
		"lib32":    {},
		"lib64":    {},
		"libx32":   {},
		"media":    {},
		"mnt":      {},
		"nix":      {},
		"proc":     {},
		"root":     {},
		"run":      {},
		"sbin":     {},
		"sessions": {},
		"srv":      {},
		"sys":      {},
		"tmp":      {},
		"usr":      {},
		"var":      {},
	}
	if entries, err := os.ReadDir("/"); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if _, ok := allowedRootEntries[name]; ok {
				continue
			}
			deny = append(deny, filepath.ToSlash(filepath.Join("/", name)))
		}
	}
	return deny
}

func normalizeSRTConfig(config srtConfig) srtConfig {
	config.Network.AllowedDomains = uniqueStrings(config.Network.AllowedDomains)
	config.Network.DeniedDomains = uniqueStrings(config.Network.DeniedDomains)
	config.Filesystem.DenyRead = uniqueStrings(cleanPaths(config.Filesystem.DenyRead))
	config.Filesystem.AllowRead = uniqueStrings(cleanPaths(config.Filesystem.AllowRead))
	config.Filesystem.AllowWrite = uniqueStrings(cleanPaths(config.Filesystem.AllowWrite))
	config.Filesystem.DenyWrite = uniqueStrings(cleanPaths(config.Filesystem.DenyWrite))
	config.Linux.BindMounts = dedupeBindMounts(config.Linux.BindMounts)
	return config
}

// isUnrestrictedNetwork returns true when the network config is effectively
// unrestricted ("*" in allowedDomains with no denied domains).
func isUnrestrictedNetwork(config srtConfig) bool {
	if len(config.Network.DeniedDomains) > 0 {
		return false
	}
	for _, d := range config.Network.AllowedDomains {
		if d == "*" {
			return true
		}
	}
	return false
}

// marshalSRTConfig serializes the SRT config to JSON. When all domains are
// allowed (allowedDomains includes "*" with no denied domains), the network
// key is omitted entirely so that SRT does not create a network namespace.
// The Claude CLI binary does not honour HTTP_PROXY env vars, so it cannot
// reach the API through SRT's proxy inside bwrap's isolated network. Omitting
// the network key keeps full filesystem sandboxing while allowing direct
// network access from the host namespace.
func marshalSRTConfig(config srtConfig) ([]byte, error) {
	if isUnrestrictedNetwork(config) {
		type noNetwork struct {
			Filesystem filesystemConfig `json:"filesystem"`
			Linux      linuxConfig      `json:"linux,omitempty"`
		}
		return json.Marshal(noNetwork{
			Filesystem: config.Filesystem,
			Linux:      config.Linux,
		})
	}
	return json.Marshal(config)
}
