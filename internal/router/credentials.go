package router

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// ResolveCredential never substitutes another identity when a reference is missing.
func ResolveCredential(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if name, ok := strings.CutPrefix(ref, "env:"); ok {
		value := strings.TrimSpace(os.Getenv(name))
		if name == "" || value == "" {
			return "", fmt.Errorf("credential environment reference is unset")
		}
		return value, nil
	}
	if path, ok := strings.CutPrefix(ref, "file:"); ok {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
			return "", fmt.Errorf("credential file is unavailable or invalid")
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return "", fmt.Errorf("credential file must be readable only by its owner")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("cannot read credential file")
		}
		value := strings.TrimSpace(string(data))
		if value == "" {
			return "", fmt.Errorf("credential file is empty")
		}
		return value, nil
	}
	return ref, nil
}
