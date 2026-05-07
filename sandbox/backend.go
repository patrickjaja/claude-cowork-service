package sandbox

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrickjaja/claude-cowork-service/native"
	"github.com/patrickjaja/claude-cowork-service/pipe"
)

const defaultSRTBinary = "srt-cowork"

// PreflightResult describes whether the sandbox backend can start here.
type PreflightResult struct {
	OK         bool
	Reason     string
	SRTPath    string
	ConfigPath string
}

// Backend runs the native Linux protocol flow, but wraps spawned commands with
// Anthropic's sandbox-runtime CLI.
type Backend struct {
	*native.Backend

	debug   bool
	srtPath string
}

// NewBackend creates a sandbox backend using the given srt binary path.
func NewBackend(srtPath string, debug bool) *Backend {
	if srtPath == "" {
		srtPath = defaultSRTBinary
	}

	b := &Backend{
		debug:   debug,
		srtPath: srtPath,
	}
	b.Backend = native.NewBackendWithOptions(debug, native.BackendOptions{
		CommandWrapper: b.wrapCommand,
	})
	return b
}

// CheckPrerequisites verifies that sandbox-runtime's srt CLI is available.
func CheckPrerequisites(srtPath string) PreflightResult {
	if srtPath == "" {
		srtPath = os.Getenv("COWORK_SANDBOX_SRT")
	}
	if srtPath == "" {
		srtPath = defaultSRTBinary
	}

	resolved, err := resolveSRT(srtPath)
	if err != nil {
		return PreflightResult{OK: false, Reason: err.Error(), SRTPath: srtPath}
	}

	configPath, err := sandboxConfigPath()
	if err != nil {
		return PreflightResult{OK: false, Reason: err.Error(), SRTPath: resolved}
	}
	if _, err := loadSandboxBaseConfigAt(configPath); err != nil {
		return PreflightResult{OK: false, Reason: err.Error(), SRTPath: resolved, ConfigPath: configPath}
	}

	return PreflightResult{OK: true, SRTPath: resolved, ConfigPath: configPath}
}

func (b *Backend) SetDebugLogging(enabled bool) {
	b.debug = enabled
	b.Backend.SetDebugLogging(enabled)
}

func (b *Backend) wrapCommand(ctx native.SpawnContext, cmd string, args []string, env map[string]string, cwd string) (string, []string, map[string]string, string, error) {
	allowedDomains, err := parseAllowedDomains(ctx.RawParams)
	if err != nil {
		return "", nil, nil, "", err
	}
	if env == nil {
		env = make(map[string]string)
	}

	if err := prepareSandboxMountTargets(ctx); err != nil {
		return "", nil, nil, "", err
	}

	baseConfig, err := loadSandboxBaseConfig()
	if err != nil {
		return "", nil, nil, "", err
	}

	hostCwd := cwd
	sandboxCwd, args, env := remapSpawnPathsForSandbox(ctx, cwd, args, env)

	config := buildSRTConfig(ctx, sandboxCwd, allowedDomains, cmd, baseConfig)
	configJSON, err := json.Marshal(config)
	if err != nil {
		return "", nil, nil, "", fmt.Errorf("sandbox: encoding srt config: %w", err)
	}

	wrappedArgs := []string{"--config-json-base64", base64.StdEncoding.EncodeToString(configJSON)}
	if b.debug {
		wrappedArgs = append(wrappedArgs, "--debug")
	}
	wrappedArgs = append(wrappedArgs, "-c", shellCommandInCwd(sandboxCwd, cmd, args))
	env["SRT_DISABLE_GLOBAL_SECCOMP"] = "1"
	if env["CLAUDE_CODE_TMPDIR"] == "" && env["CLAUDE_TMPDIR"] == "" {
		env["CLAUDE_CODE_TMPDIR"] = "/tmp"
	}

	if b.debug {
		log.Printf("[sandbox] spawn via %s config-bytes=%d cwd=%s command=%s", b.srtPath, len(configJSON), sandboxCwd, shellCommand(cmd, args))
	}
	return b.srtPath, wrappedArgs, env, hostCwd, nil
}

type spawnParams struct {
	AllowedDomains []string `json:"allowedDomains"`
}

type srtConfig struct {
	Network    networkConfig    `json:"network" yaml:"network"`
	Filesystem filesystemConfig `json:"filesystem" yaml:"filesystem"`
	Linux      linuxConfig      `json:"linux,omitempty" yaml:"linux"`
}

type networkConfig struct {
	AllowedDomains []string `json:"allowedDomains" yaml:"allowedDomains"`
	DeniedDomains  []string `json:"deniedDomains" yaml:"deniedDomains"`
}

type filesystemConfig struct {
	DenyRead   []string `json:"denyRead" yaml:"denyRead"`
	AllowRead  []string `json:"allowRead" yaml:"allowRead"`
	AllowWrite []string `json:"allowWrite" yaml:"allowWrite"`
	DenyWrite  []string `json:"denyWrite" yaml:"denyWrite"`
}

type linuxConfig struct {
	BindMounts []bindMountConfig `json:"bindMounts,omitempty" yaml:"bindMounts"`
}

type bindMountConfig struct {
	Source string `json:"source" yaml:"source"`
	Target string `json:"target" yaml:"target"`
	Mode   string `json:"mode,omitempty" yaml:"mode"`
}

func parseAllowedDomains(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	var p spawnParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("sandbox: parsing spawn params: %w", err)
	}
	return uniqueStrings(p.AllowedDomains), nil
}

func buildSRTConfig(ctx native.SpawnContext, cwd string, allowedDomains []string, cmd string, base srtConfig) srtConfig {
	config := base
	config.Network.AllowedDomains = append(config.Network.AllowedDomains, allowedDomains...)
	config.Filesystem.AllowRead = append(config.Filesystem.AllowRead, commandReadAllowPaths(cmd)...)
	config.Filesystem.AllowRead = append(config.Filesystem.AllowRead, ctx.SessionPrefix, "/mnt/.virtiofs-root")
	bindMounts := []bindMountConfig{
		{
			Source: ctx.RealSessionDir,
			Target: ctx.SessionPrefix,
			Mode:   "rw",
		},
	}

	if cwd != "" && !isUnderReadonlyMount(cwd, ctx.ResolvedMounts) && !isSyntheticSandboxPath(cwd, ctx.SessionPrefix) {
		config.Filesystem.AllowWrite = append(config.Filesystem.AllowWrite, cwd)
	}

	names := make([]string, 0, len(ctx.ResolvedMounts))
	for name := range ctx.ResolvedMounts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		mount := ctx.ResolvedMounts[name]
		if mount.Path == "" {
			continue
		}
		path := filepath.Clean(mount.Path)
		if !mountSourceIsDirectory(path) {
			continue
		}
		mode := bindMode(mount.Mode)
		if !sessionRootProvidesMount(ctx, name, path) {
			bindMounts = append(bindMounts, bindMountConfig{
				Source: path,
				Target: filepath.ToSlash(filepath.Join(ctx.SessionPrefix, "mnt", name)),
				Mode:   mode,
			})
		}
		bindMounts = append(bindMounts,
			bindMountConfig{
				Source: path,
				Target: sharedTarget(path),
				Mode:   mode,
			},
		)

		// Mount writeability is enforced by the custom bind mount mode. Do not
		// add host source paths to SRT's global write allow-list, because that
		// would expose them at their original host paths in addition to the VM
		// compatibility paths.
	}
	config.Linux.BindMounts = append(config.Linux.BindMounts, bindMounts...)

	return normalizeSRTConfig(config)
}

func resolveSRT(srtPath string) (string, error) {
	if strings.ContainsRune(srtPath, os.PathSeparator) {
		info, err := os.Stat(srtPath)
		if err != nil {
			return "", fmt.Errorf("srt binary %q not found: %w", srtPath, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("srt binary %q is a directory", srtPath)
		}
		if info.Mode()&0o111 == 0 {
			return "", fmt.Errorf("srt binary %q is not executable", srtPath)
		}
		return srtPath, nil
	}

	if resolved, err := exec.LookPath(srtPath); err == nil {
		return resolved, nil
	}
	if out, err := exec.Command("bash", "-lc", "command -v "+shellArg(srtPath)).Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			resolved := strings.TrimSpace(line)
			if strings.HasPrefix(resolved, "/") && !strings.ContainsAny(resolved, " \t") {
				return resolved, nil
			}
		}
	}
	return "", fmt.Errorf("srt binary %q not found; install @anthropic-ai/sandbox-runtime or set COWORK_SANDBOX_SRT", srtPath)
}

func shellCommand(cmd string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellArg(cmd))
	for _, arg := range args {
		parts = append(parts, shellArg(arg))
	}
	return strings.Join(parts, " ")
}

func shellCommandInCwd(cwd string, cmd string, args []string) string {
	command := "exec " + shellCommand(cmd, args)
	if cwd == "" {
		return command
	}
	return "cd " + shellArg(cwd) + " && " + command
}

func shellArg(arg string) string {
	if arg == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}

func isWritableMountMode(mode string) bool {
	return !strings.HasPrefix(strings.ToLower(strings.TrimSpace(mode)), "ro")
}

func bindMode(mode string) string {
	if isWritableMountMode(mode) {
		return "rw"
	}
	return "ro"
}

func prepareSandboxMountTargets(ctx native.SpawnContext) error {
	mntDir := filepath.Join(ctx.RealSessionDir, "mnt")
	names := make([]string, 0, len(ctx.ResolvedMounts))
	for name := range ctx.ResolvedMounts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		iDepth := strings.Count(filepath.Clean(names[i]), string(filepath.Separator))
		jDepth := strings.Count(filepath.Clean(names[j]), string(filepath.Separator))
		if iDepth != jDepth {
			return iDepth < jDepth
		}
		return names[i] < names[j]
	})

	for _, name := range names {
		mount := ctx.ResolvedMounts[name]
		if mount.Path == "" || !mountSourceIsDirectory(mount.Path) {
			continue
		}

		target := filepath.Join(mntDir, name)
		if info, err := os.Lstat(target); err == nil {
			if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			if err := os.Remove(target); err != nil {
				return fmt.Errorf("sandbox: replacing mount target %s: %w", target, err)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("sandbox: checking mount target %s: %w", target, err)
		}

		if err := os.MkdirAll(target, 0755); err != nil {
			return fmt.Errorf("sandbox: creating mount target %s: %w", target, err)
		}
	}
	return nil
}

func mountSourceIsDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func commandReadAllowPaths(cmd string) []string {
	if cmd == "" || !filepath.IsAbs(cmd) {
		return nil
	}

	allow := []string{filepath.Clean(cmd)}
	if resolved, err := filepath.EvalSymlinks(cmd); err == nil {
		allow = append(allow, resolved)
	}
	return allow
}

func isSyntheticSandboxPath(path string, sessionPrefix string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	return path == sessionPrefix ||
		strings.HasPrefix(path, sessionPrefix+"/") ||
		path == "/mnt/.virtiofs-root" ||
		strings.HasPrefix(path, "/mnt/.virtiofs-root/")
}

func remapSpawnPathsForSandbox(ctx native.SpawnContext, cwd string, args []string, env map[string]string) (string, []string, map[string]string) {
	replacements := sandboxPathReplacements(ctx)
	cwd = replacePathPrefix(cwd, replacements)
	for i, arg := range args {
		args[i] = replaceAllPathPrefixes(arg, replacements)
	}
	for key, value := range env {
		env[key] = replaceAllPathPrefixes(value, replacements)
	}
	return cwd, args, env
}

type pathReplacement struct {
	from string
	to   string
}

func sandboxPathReplacements(ctx native.SpawnContext) []pathReplacement {
	replacements := []pathReplacement{{
		from: filepath.Clean(ctx.RealSessionDir),
		to:   ctx.SessionPrefix,
	}}

	names := make([]string, 0, len(ctx.ResolvedMounts))
	for name := range ctx.ResolvedMounts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		mount := ctx.ResolvedMounts[name]
		if mount.Path == "" {
			continue
		}
		replacements = append(replacements, pathReplacement{
			from: filepath.Clean(mount.Path),
			to:   filepath.ToSlash(filepath.Join(ctx.SessionPrefix, "mnt", name)),
		})
	}
	sort.SliceStable(replacements, func(i, j int) bool {
		return len(replacements[i].from) > len(replacements[j].from)
	})
	return replacements
}

func replacePathPrefix(value string, replacements []pathReplacement) string {
	for _, replacement := range replacements {
		if value == replacement.from {
			return replacement.to
		}
		if strings.HasPrefix(value, replacement.from+string(filepath.Separator)) {
			return replacement.to + value[len(replacement.from):]
		}
	}
	return value
}

func replaceAllPathPrefixes(value string, replacements []pathReplacement) string {
	for _, replacement := range replacements {
		value = strings.ReplaceAll(value, replacement.from+string(filepath.Separator), replacement.to+"/")
		if value == replacement.from {
			value = replacement.to
		}
	}
	return value
}

func sharedTarget(hostPath string) string {
	rel := strings.TrimPrefix(filepath.Clean(hostPath), string(filepath.Separator))
	return filepath.ToSlash(filepath.Join("/mnt/.virtiofs-root", "shared", rel))
}

func dedupeBindMounts(bindMounts []bindMountConfig) []bindMountConfig {
	seen := make(map[string]struct{}, len(bindMounts))
	out := make([]bindMountConfig, 0, len(bindMounts))
	for _, mount := range bindMounts {
		if mount.Source == "" || mount.Target == "" {
			continue
		}
		key := mount.Source + "\x00" + mount.Target
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, mount)
	}
	return out
}

func isUnderReadonlyMount(path string, mounts map[string]native.ResolvedMountSpec) bool {
	for _, mount := range mounts {
		if mount.Path == "" || isWritableMountMode(mount.Mode) {
			continue
		}
		if pathWithin(filepath.Clean(mount.Path), filepath.Clean(path)) {
			return true
		}
	}
	return false
}

func sessionRootProvidesMount(ctx native.SpawnContext, name string, hostPath string) bool {
	linkPath := filepath.Join(ctx.RealSessionDir, "mnt", name)
	resolved, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		return false
	}
	resolvedHostPath, err := filepath.EvalSymlinks(hostPath)
	if err != nil {
		resolvedHostPath = hostPath
	}
	return filepath.Clean(resolved) == filepath.Clean(resolvedHostPath)
}

func pathWithin(parent string, child string) bool {
	if parent == "" || child == "" {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func cleanPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		out = append(out, filepath.Clean(path))
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

var _ pipe.VMBackend = (*Backend)(nil)
