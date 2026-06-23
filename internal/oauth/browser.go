package oauth

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenBrowser launches the OS default browser at url. It is the real opener passed to LoginPKCE; a
// failure is non-fatal (the caller has already printed the URL for a manual paste). Best-effort by
// design — headless boxes simply have no browser, which is what `--device` is for.
func OpenBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default: // linux, *bsd, …
		cmd = "xdg-open"
		args = []string{url}
	}
	if _, err := exec.LookPath(cmd); err != nil {
		return fmt.Errorf("no browser launcher (%s) found; open the URL manually", cmd)
	}
	return exec.Command(cmd, args...).Start()
}
