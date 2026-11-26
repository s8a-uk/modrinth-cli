//go:build windows

package config

// permWarning does nothing on Windows: file modes do not map to ACLs, and the
// files under %AppData% inherit an ACL that only the user and administrators can read.
func permWarning(string) string { return "" }
