package sandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/patrickjaja/claude-cowork-service/native"
)

func TestShellCommandQuotesArguments(t *testing.T) {
	got := shellCommand("/usr/bin/echo", []string{"hello world", "it's ok", "$(touch nope)", ""})
	want := "'/usr/bin/echo' 'hello world' 'it'\\''s ok' '$(touch nope)' ''"
	if got != want {
		t.Fatalf("shellCommand() = %q, want %q", got, want)
	}
}

func TestShellCommandInCwd(t *testing.T) {
	got := shellCommandInCwd("/sessions/test workspace", "/usr/bin/echo", []string{"hello"})
	want := "cd '/sessions/test workspace' && exec '/usr/bin/echo' 'hello'"
	if got != want {
		t.Fatalf("shellCommandInCwd() = %q, want %q", got, want)
	}
}

func TestWrapCommandHandlesNilEnv(t *testing.T) {
	t.Setenv(sandboxConfigEnv, filepath.Join(t.TempDir(), "sandbox.yaml"))
	b := NewBackend("/usr/bin/srt-cowork", false)
	_, _, env, _, err := b.wrapCommand(native.SpawnContext{
		RealSessionDir: t.TempDir(),
		SessionPrefix:  "/sessions/test-session",
		RawParams:      []byte(`{"allowedDomains":[]}`),
	}, "/usr/bin/echo", []string{"hello"}, nil, t.TempDir())
	if err != nil {
		t.Fatalf("wrapCommand returned error: %v", err)
	}
	if env["SRT_DISABLE_GLOBAL_SECCOMP"] != "1" {
		t.Fatalf("SRT_DISABLE_GLOBAL_SECCOMP = %q", env["SRT_DISABLE_GLOBAL_SECCOMP"])
	}
	if env["CLAUDE_CODE_TMPDIR"] != "/tmp" {
		t.Fatalf("CLAUDE_CODE_TMPDIR = %q", env["CLAUDE_CODE_TMPDIR"])
	}
}

func TestCheckPrerequisitesCreatesSandboxConfig(t *testing.T) {
	tmp := t.TempDir()
	srtPath := filepath.Join(tmp, "srt-cowork")
	configPath := filepath.Join(tmp, "config", "sandbox.yaml")
	t.Setenv(sandboxConfigEnv, configPath)

	if err := os.WriteFile(srtPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	check := CheckPrerequisites(srtPath)
	if !check.OK {
		t.Fatalf("CheckPrerequisites failed: %s", check.Reason)
	}
	if check.ConfigPath != configPath {
		t.Fatalf("ConfigPath = %q, want %q", check.ConfigPath, configPath)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("default config was not created during preflight: %v", err)
	}
}

func TestBuildSRTConfigUsesAllowedDomainsAndWritableMounts(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "session")
	workspace := filepath.Join(t.TempDir(), "workspace")
	uploads := filepath.Join(t.TempDir(), "uploads")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(uploads, 0755); err != nil {
		t.Fatal(err)
	}

	config := buildSRTConfig(native.SpawnContext{
		RealSessionDir: sessionDir,
		SessionPrefix:  "/sessions/test-session",
		ResolvedMounts: map[string]native.ResolvedMountSpec{
			"workspace": {Path: workspace, Mode: "rw"},
			"uploads":   {Path: uploads, Mode: "ro"},
		},
	}, "/sessions/test-session/mnt/workspace", []string{"api.anthropic.com", "api.anthropic.com", "github.com"}, "/usr/bin/bash", defaultSandboxBaseConfig())

	if !reflect.DeepEqual(config.Network.AllowedDomains, []string{"api.anthropic.com", "github.com"}) {
		t.Fatalf("allowed domains = %#v", config.Network.AllowedDomains)
	}
	if !contains(config.Filesystem.DenyRead, "/home") {
		t.Fatalf("denyRead missing /home: %#v", config.Filesystem.DenyRead)
	}
	if !contains(config.Filesystem.DenyRead, filepath.ToSlash(filepath.Join("/run/user", strconv.Itoa(os.Getuid())))) {
		t.Fatalf("denyRead missing current XDG runtime dir: %#v", config.Filesystem.DenyRead)
	}
	if !contains(config.Filesystem.DenyRead, "/tmp") || !contains(config.Filesystem.DenyRead, "/var/tmp") {
		t.Fatalf("denyRead missing tmpfs-backed temp dirs: %#v", config.Filesystem.DenyRead)
	}
	if !contains(config.Filesystem.DenyRead, "/var") || !contains(config.Filesystem.AllowRead, "/var/lib") {
		t.Fatalf("config does not deny /var while reallowing /var/lib: %#v / %#v", config.Filesystem.DenyRead, config.Filesystem.AllowRead)
	}
	if _, err := os.Stat("/dpool"); err == nil && !contains(config.Filesystem.DenyRead, "/dpool") {
		t.Fatalf("denyRead missing /dpool: %#v", config.Filesystem.DenyRead)
	}
	if contains(config.Filesystem.AllowWrite, sessionDir) || contains(config.Filesystem.AllowWrite, workspace) {
		t.Fatalf("allowWrite exposes original host paths: %#v", config.Filesystem.AllowWrite)
	}
	if contains(config.Filesystem.DenyWrite, uploads) {
		t.Fatalf("denyWrite contains original host readonly mount: %#v", config.Filesystem.DenyWrite)
	}
	if !containsBind(config.Linux.BindMounts, sessionDir, "/sessions/test-session", "rw") {
		t.Fatalf("bindMounts missing session root: %#v", config.Linux.BindMounts)
	}
	if !containsBind(config.Linux.BindMounts, workspace, "/sessions/test-session/mnt/workspace", "rw") {
		t.Fatalf("bindMounts missing workspace session mount: %#v", config.Linux.BindMounts)
	}
	if !containsBind(config.Linux.BindMounts, uploads, "/sessions/test-session/mnt/uploads", "ro") {
		t.Fatalf("bindMounts missing readonly upload session mount: %#v", config.Linux.BindMounts)
	}
	if !containsBind(config.Linux.BindMounts, workspace, sharedTarget(workspace), "rw") {
		t.Fatalf("bindMounts missing workspace shared mount: %#v", config.Linux.BindMounts)
	}
}

func TestBuildSRTConfigBindsSessionMountAfterPreparingSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "session")
	mntDir := filepath.Join(sessionDir, "mnt")
	memory := filepath.Join(root, "memory")
	if err := os.MkdirAll(mntDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(memory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(memory, filepath.Join(mntDir, ".auto-memory")); err != nil {
		t.Fatal(err)
	}

	ctx := native.SpawnContext{
		RealSessionDir: sessionDir,
		SessionPrefix:  "/sessions/test-session",
		ResolvedMounts: map[string]native.ResolvedMountSpec{
			".auto-memory": {Path: memory, Mode: "rw"},
		},
	}
	if err := prepareSandboxMountTargets(ctx); err != nil {
		t.Fatal(err)
	}
	config := buildSRTConfig(ctx, "", nil, "/usr/bin/bash", defaultSandboxBaseConfig())

	targetInfo, err := os.Lstat(filepath.Join(mntDir, ".auto-memory"))
	if err != nil {
		t.Fatal(err)
	}
	if !targetInfo.IsDir() || targetInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("prepared mount target is not a real directory: %v", targetInfo.Mode())
	}
	if !containsBind(config.Linux.BindMounts, memory, "/sessions/test-session/mnt/.auto-memory", "rw") {
		t.Fatalf("bindMounts missing prepared session mount: %#v", config.Linux.BindMounts)
	}
	if !containsBind(config.Linux.BindMounts, memory, sharedTarget(memory), "rw") {
		t.Fatalf("bindMounts missing shared memory mount: %#v", config.Linux.BindMounts)
	}
}

func TestBuildSRTConfigDoesNotAllowReadonlyCwd(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "session")
	readonly := filepath.Join(t.TempDir(), "readonly")

	config := buildSRTConfig(native.SpawnContext{
		RealSessionDir: sessionDir,
		SessionPrefix:  "/sessions/test-session",
		ResolvedMounts: map[string]native.ResolvedMountSpec{
			"readonly": {Path: readonly, Mode: "ro"},
		},
	}, filepath.Join(readonly, "child"), nil, "/usr/bin/bash", defaultSandboxBaseConfig())

	if contains(config.Filesystem.AllowWrite, filepath.Join(readonly, "child")) {
		t.Fatalf("allowWrite contains cwd under read-only mount: %#v", config.Filesystem.AllowWrite)
	}
}

func TestLoadSandboxBaseConfigCreatesDefaultYAML(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "sandbox.yaml")
	t.Setenv(sandboxConfigEnv, configPath)

	config, err := loadSandboxBaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !contains(config.Filesystem.DenyRead, "/tmp") || !contains(config.Filesystem.DenyRead, "/var/tmp") {
		t.Fatalf("default config missing tmpfs temp denies: %#v", config.Filesystem.DenyRead)
	}
	if !contains(config.Filesystem.DenyRead, "/var") || !contains(config.Filesystem.AllowRead, "/var/lib") {
		t.Fatalf("default config missing /var baseline: %#v / %#v", config.Filesystem.DenyRead, config.Filesystem.AllowRead)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("default config file was not written: %v", err)
	}
}

func TestSandboxBaseConfigExtendsPerSpawnConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "sandbox.yaml")
	t.Setenv(sandboxConfigEnv, configPath)
	if err := os.WriteFile(configPath, []byte(`
network:
  allowedDomains:
    - baseline.example
filesystem:
  denyRead:
    - /secret
  allowRead:
    - /var/lib
  allowWrite:
    - /custom-rw
  denyWrite: []
linux:
  bindMounts:
    - source: /baseline-source
      target: /baseline-target
      mode: ro
`), 0644); err != nil {
		t.Fatal(err)
	}

	base, err := loadSandboxBaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(t.TempDir(), "session")
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}

	config := buildSRTConfig(native.SpawnContext{
		RealSessionDir: sessionDir,
		SessionPrefix:  "/sessions/test-session",
		ResolvedMounts: map[string]native.ResolvedMountSpec{
			"workspace": {Path: workspace, Mode: "rw"},
		},
	}, "/sessions/test-session/mnt/workspace", []string{"spawn.example"}, "/usr/bin/bash", base)

	if !reflect.DeepEqual(config.Network.AllowedDomains, []string{"baseline.example", "spawn.example"}) {
		t.Fatalf("allowed domains = %#v", config.Network.AllowedDomains)
	}
	if !contains(config.Filesystem.DenyRead, "/secret") || !contains(config.Filesystem.AllowWrite, "/custom-rw") {
		t.Fatalf("filesystem baseline not preserved: %#v", config.Filesystem)
	}
	if !containsBind(config.Linux.BindMounts, "/baseline-source", "/baseline-target", "ro") {
		t.Fatalf("baseline bind mount missing: %#v", config.Linux.BindMounts)
	}
	if !containsBind(config.Linux.BindMounts, workspace, "/sessions/test-session/mnt/workspace", "rw") {
		t.Fatalf("spawn bind mount missing: %#v", config.Linux.BindMounts)
	}
}

func TestSandboxBaseConfigPropagatesAllowAllUnixSockets(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "sandbox.yaml")
	t.Setenv(sandboxConfigEnv, configPath)
	if err := os.WriteFile(configPath, []byte(`
network:
  allowedDomains: []
  deniedDomains: []
  allowAllUnixSockets: true
filesystem:
  denyRead: []
  allowRead: []
  allowWrite: []
  denyWrite: []
linux:
  bindMounts: []
`), 0644); err != nil {
		t.Fatal(err)
	}

	base, err := loadSandboxBaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !base.Network.AllowAllUnixSockets {
		t.Fatalf("base config did not load allowAllUnixSockets")
	}

	config := buildSRTConfig(native.SpawnContext{
		RealSessionDir: t.TempDir(),
		SessionPrefix:  "/sessions/test-session",
	}, "/sessions/test-session", nil, "/usr/bin/bash", base)
	if !config.Network.AllowAllUnixSockets {
		t.Fatalf("buildSRTConfig dropped allowAllUnixSockets: %#v", config.Network)
	}
}

func TestSandboxDefaultConfigOmitsAllowAllUnixSockets(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "sandbox.yaml")
	t.Setenv(sandboxConfigEnv, configPath)
	base, err := loadSandboxBaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	if base.Network.AllowAllUnixSockets {
		t.Fatalf("default config must keep allowAllUnixSockets disabled")
	}
	yamlBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(yamlBytes), "allowAllUnixSockets: false") {
		t.Fatalf("default config YAML missing explicit allowAllUnixSockets line:\n%s", yamlBytes)
	}
}

func TestRemapSpawnPathsForSandbox(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "session")
	workspace := filepath.Join(t.TempDir(), "workspace")
	ctx := native.SpawnContext{
		RealSessionDir: sessionDir,
		SessionPrefix:  "/sessions/test-session",
		ResolvedMounts: map[string]native.ResolvedMountSpec{
			"workspace": {Path: workspace, Mode: "rw"},
		},
	}
	env := map[string]string{
		"WORKSPACE": filepath.Join(workspace, "src"),
	}

	cwd, args, env := remapSpawnPathsForSandbox(ctx,
		filepath.Join(workspace, "src"),
		[]string{"--path", filepath.Join(workspace, "README.md")},
		env,
	)

	if cwd != "/sessions/test-session/mnt/workspace/src" {
		t.Fatalf("cwd = %q", cwd)
	}
	if args[1] != "/sessions/test-session/mnt/workspace/README.md" {
		t.Fatalf("args[1] = %q", args[1])
	}
	if env["WORKSPACE"] != "/sessions/test-session/mnt/workspace/src" {
		t.Fatalf("WORKSPACE = %q", env["WORKSPACE"])
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsBind(values []bindMountConfig, source string, target string, mode string) bool {
	for _, value := range values {
		if value.Source == source && value.Target == target && value.Mode == mode {
			return true
		}
	}
	return false
}
