// Package bwrap provides a builder for bubblewrap (bwrap) sandbox commands.
// Used by both exec tool and browser sandboxing.
package bwrap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// Builder constructs bwrap command arguments using a fluent interface.
type Builder struct {
	args        []string
	bwrapPath   string
	command     string
	commandArgs []string
	err         error
}

// New creates a new bwrap command builder.
func New() *Builder {
	return &Builder{
		args: make([]string, 0, 64),
	}
}

// BwrapPath sets a custom path to the bwrap binary.
// If not set, FindBwrap() is used to locate it.
func (b *Builder) BwrapPath(path string) *Builder {
	b.bwrapPath = path
	return b
}

// SystemBinds adds read-only binds for system directories (/usr, /lib, /bin, /sbin).
// Automatically handles /lib64 if it exists.
func (b *Builder) SystemBinds() *Builder {
	paths := []string{"/usr", "/lib", "/bin", "/sbin"}
	for _, p := range paths {
		if pathExists(p) {
			b.args = append(b.args, "--ro-bind", p, p)
		}
	}
	// /lib64 for 64-bit systems
	if pathExists("/lib64") {
		b.args = append(b.args, "--ro-bind", "/lib64", "/lib64")
	}
	return b
}

// EtcBinds adds read-only binds for essential /etc files needed for basic operation.
func (b *Builder) EtcBinds() *Builder {
	files := []string{
		"/etc/resolv.conf",
		"/etc/hosts",
		"/etc/passwd",
		"/etc/group",
		"/etc/nsswitch.conf",
		"/etc/localtime",
	}
	for _, f := range files {
		if pathExists(f) {
			b.args = append(b.args, "--ro-bind", f, f)
		}
	}
	return b
}

// SSLCerts adds read-only binds for SSL certificate directories (distro-specific).
func (b *Builder) SSLCerts() *Builder {
	paths := []string{
		"/etc/ssl",
		"/etc/ca-certificates", // Debian/Ubuntu
		"/etc/pki",             // RHEL/Fedora
	}
	for _, p := range paths {
		if pathExists(p) {
			b.args = append(b.args, "--ro-bind", p, p)
		}
	}
	return b
}

// Fonts adds read-only binds for font directories.
func (b *Builder) Fonts() *Builder {
	paths := []string{
		"/etc/fonts",
		"/usr/share/fonts",
	}
	for _, p := range paths {
		if pathExists(p) {
			b.args = append(b.args, "--ro-bind", p, p)
		}
	}
	return b
}

// RoBind adds a read-only bind mount.
func (b *Builder) RoBind(path string) *Builder {
	if pathExists(path) {
		b.args = append(b.args, "--ro-bind", path, path)
	}
	return b
}

// RoBindTo adds a read-only bind mount with a different destination path.
func (b *Builder) RoBindTo(src, dst string) *Builder {
	if pathExists(src) {
		b.args = append(b.args, "--ro-bind", src, dst)
	}
	return b
}

// Bind adds a read-write bind mount.
func (b *Builder) Bind(path string) *Builder {
	if pathExists(path) {
		b.args = append(b.args, "--bind", path, path)
	}
	return b
}

// BindTo adds a read-write bind mount with a different destination path.
func (b *Builder) BindTo(src, dst string) *Builder {
	if pathExists(src) {
		b.args = append(b.args, "--bind", src, dst)
	}
	return b
}

// Tmpfs adds a tmpfs mount at the given path.
func (b *Builder) Tmpfs(path string) *Builder {
	b.args = append(b.args, "--tmpfs", path)
	return b
}

// Proc mounts /proc.
func (b *Builder) Proc() *Builder {
	b.args = append(b.args, "--proc", "/proc")
	return b
}

// Dev mounts /dev.
func (b *Builder) Dev() *Builder {
	b.args = append(b.args, "--dev", "/dev")
	return b
}

// DevBind adds a device bind mount (for /dev/shm, /dev/dri, etc.).
func (b *Builder) DevBind(path string) *Builder {
	if pathExists(path) {
		b.args = append(b.args, "--dev-bind", path, path)
	}
	return b
}

// SharedMem binds /dev/shm (required for Chromium IPC).
func (b *Builder) SharedMem() *Builder {
	return b.DevBind("/dev/shm")
}

// GPU binds /dev/dri for GPU acceleration.
func (b *Builder) GPU() *Builder {
	return b.DevBind("/dev/dri")
}

// ShareNet shares the network namespace with the host.
func (b *Builder) ShareNet() *Builder {
	b.args = append(b.args, "--share-net")
	return b
}

// UnshareNet creates an isolated network namespace (no network access).
func (b *Builder) UnshareNet() *Builder {
	b.args = append(b.args, "--unshare-net")
	return b
}

// UnsharePID creates an isolated PID namespace.
func (b *Builder) UnsharePID() *Builder {
	b.args = append(b.args, "--unshare-pid")
	return b
}

// DieWithParent ensures sandbox is killed when parent (GoClaw) dies.
func (b *Builder) DieWithParent() *Builder {
	b.args = append(b.args, "--die-with-parent")
	return b
}

// ClearEnv clears all environment variables (security: prevents AWS_SECRET_ACCESS_KEY etc from leaking).
func (b *Builder) ClearEnv() *Builder {
	b.args = append(b.args, "--clearenv")
	return b
}

// SetEnv sets an environment variable in the sandbox.
func (b *Builder) SetEnv(key, value string) *Builder {
	b.args = append(b.args, "--setenv", key, value)
	return b
}

// Chdir sets the working directory inside the sandbox.
func (b *Builder) Chdir(path string) *Builder {
	b.args = append(b.args, "--chdir", path)
	return b
}

// Display sets up X11 display access (for headed browser mode).
func (b *Builder) Display() *Builder {
	display := os.Getenv("DISPLAY")
	if display != "" {
		b.SetEnv("DISPLAY", display)
		// X11 socket
		if pathExists("/tmp/.X11-unix") {
			b.args = append(b.args, "--ro-bind", "/tmp/.X11-unix", "/tmp/.X11-unix")
		}
		// X11 authentication (XAUTHORITY can be anywhere, e.g., /run/user/1000/xauth_*)
		if xauth := os.Getenv("XAUTHORITY"); xauth != "" && pathExists(xauth) {
			b.SetEnv("XAUTHORITY", xauth)
			b.args = append(b.args, "--ro-bind", xauth, xauth)
		}
	}
	return b
}

// Wayland sets up Wayland display access.
func (b *Builder) Wayland() *Builder {
	waylandDisplay := os.Getenv("WAYLAND_DISPLAY")
	if waylandDisplay != "" {
		b.SetEnv("WAYLAND_DISPLAY", waylandDisplay)
		xdgRuntime := os.Getenv("XDG_RUNTIME_DIR")
		if xdgRuntime != "" {
			waylandSocket := filepath.Join(xdgRuntime, waylandDisplay)
			if pathExists(waylandSocket) {
				b.args = append(b.args, "--ro-bind", waylandSocket, "/tmp/"+waylandDisplay)
			}
		}
	}
	return b
}

// Dbus binds the D-Bus system socket for applications that need it.
// Optional but needed for some Chromium features in headed mode.
func (b *Builder) Dbus() *Builder {
	// System bus
	if pathExists("/run/dbus/system_bus_socket") {
		b.args = append(b.args, "--ro-bind", "/run/dbus/system_bus_socket", "/run/dbus/system_bus_socket")
	}
	// Session bus (if available)
	if xdgRuntime := os.Getenv("XDG_RUNTIME_DIR"); xdgRuntime != "" {
		sessionBus := filepath.Join(xdgRuntime, "bus")
		if pathExists(sessionBus) {
			b.args = append(b.args, "--ro-bind", sessionBus, sessionBus)
		}
	}
	return b
}

// DefaultEnv sets minimal required environment variables (PATH, TERM, LANG, USER).
// Should be called after ClearEnv().
// path is the complete PATH string to use (built by caller, e.g., sandbox manager).
// If path is empty, falls back to basic system PATH.
func (b *Builder) DefaultEnv(home string, path string) *Builder {
	if path == "" {
		path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}

	b.SetEnv("PATH", path)
	b.SetEnv("HOME", home)
	b.SetEnv("TERM", "xterm")

	if lang := os.Getenv("LANG"); lang != "" {
		b.SetEnv("LANG", lang)
	} else {
		b.SetEnv("LANG", "C.UTF-8")
	}

	if user := os.Getenv("USER"); user != "" {
		b.SetEnv("USER", user)
	}

	return b
}

// Command sets the command to run inside the sandbox.
func (b *Builder) Command(cmd string, args ...string) *Builder {
	b.command = cmd
	b.commandArgs = args
	return b
}

// ShellCommand sets a shell command to run (sh -c "command").
func (b *Builder) ShellCommand(cmd string) *Builder {
	b.command = "sh"
	b.commandArgs = []string{"-c", cmd}
	return b
}

// Build returns the complete argument list for exec.Command.
// Returns (bwrapPath, args, error).
func (b *Builder) Build() (string, []string, error) {
	if b.err != nil {
		return "", nil, b.err
	}

	// Find bwrap binary
	bwrapPath := b.bwrapPath
	if bwrapPath == "" {
		var err error
		bwrapPath, err = FindBwrap("")
		if err != nil {
			return "", nil, err
		}
	}

	// Build final args
	args := make([]string, 0, len(b.args)+3+len(b.commandArgs))
	args = append(args, b.args...)
	args = append(args, "--")
	args = append(args, b.command)
	args = append(args, b.commandArgs...)

	return bwrapPath, args, nil
}

// BuildCommand builds and returns an exec.Cmd ready to run.
func (b *Builder) BuildCommand() (*exec.Cmd, error) {
	bwrapPath, args, err := b.Build()
	if err != nil {
		return nil, err
	}
	return exec.Command(bwrapPath, args...), nil //nolint:gosec // G204: bwrapPath validated by FindBwrap()
}

// FindBwrap locates the bwrap binary.
// If customPath is provided and exists, it's used.
// Otherwise searches PATH.
func FindBwrap(customPath string) (string, error) {
	if customPath != "" {
		if pathExists(customPath) {
			return customPath, nil
		}
		L_warn("bwrap: custom path not found", "path", customPath)
	}

	path, err := exec.LookPath("bwrap")
	if err != nil {
		return "", fmt.Errorf(`sandbox enabled but bwrap not found

Install bubblewrap:
  Debian/Ubuntu:  apt install bubblewrap
  Fedora/RHEL:    dnf install bubblewrap
  Arch:           pacman -S bubblewrap

Or disable sandbox in config`)
	}

	return path, nil
}

// IsAvailable checks if bwrap is available on this system.
func IsAvailable(customPath string) bool {
	_, err := FindBwrap(customPath)
	return err == nil
}

// IsLinux returns true if running on Linux.
func IsLinux() bool {
	return runtime.GOOS == "linux"
}

// pathExists checks if a path exists.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
